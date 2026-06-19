package middleware

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// Bộ giới hạn tốc độ RateLimiter
type RateLimiter struct {
	enabled   bool
	limiters  map[string]*rate.Limiter
	mu        sync.RWMutex
	r         rate.Limit
	b         int
	maxConns  int
	connCount int32
	connMu    sync.Mutex
}

// NewRateLimiter tạo ra một bộ giới hạn tốc độ mới
func NewRateLimiter(enabled bool, requestsPerSecond int, burstSize int, maxConnections int) *RateLimiter {
	return &RateLimiter{
		enabled:  enabled,
		limiters: make(map[string]*rate.Limiter),
		r:        rate.Limit(requestsPerSecond),
		b:        burstSize,
		maxConns: maxConnections,
	}
}

// getLimiter Nhận hoặc tạo bộ giới hạn IP
func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, exists := rl.limiters[ip]
	if !exists {
		limiter = rate.NewLimiter(rl.r, rl.b)
		rl.limiters[ip] = limiter
	}

	return limiter
}

// cleanupLimiters dọn dẹp các bộ giới hạn đã hết hạn
func (rl *RateLimiter) cleanupLimiters() {
	ticker := time.NewTicker(time.Minute)
	go func() {
		for range ticker.C {
			rl.mu.Lock()
			for ip, limiter := range rl.limiters {
				if limiter.Allow() {
					// Nếu bộ giới hạn cho phép yêu cầu thì có thể nó đã lâu không được sử dụng, hãy xóa nó đi
					delete(rl.limiters, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
}

// Phần mềm trung gian giới hạn tốc độ phần mềm trung gian
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	// Nếu bộ giới hạn tốc độ không được bật, hãy trực tiếp bỏ qua nó
	if !rl.enabled {
		return next
	}

	// Bắt đầu coroutine dọn dẹp
	rl.cleanupLimiters()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Kiểm tra giới hạn kết nối
		rl.connMu.Lock()
		if rl.connCount >= int32(rl.maxConns) {
			rl.connMu.Unlock()
			http.Error(w, "Too many connections", http.StatusTooManyRequests)
			return
		}
		rl.connCount++
		rl.connMu.Unlock()

		// Số lượng giảm khi kết nối kết thúc
		defer func() {
			rl.connMu.Lock()
			rl.connCount--
			rl.connMu.Unlock()
		}()

		// Nhận IP của khách hàng
		ip := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			ip = forwarded
		}

		// Kiểm tra giới hạn tỷ lệ
		limiter := rl.getLimiter(ip)
		if !limiter.Allow() {
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// GetStatsNhận số liệu thống kê
func (rl *RateLimiter) GetStats() map[string]interface{} {
	// Nhận số lượng kết nối bằng các hoạt động nguyên tử
	currentConns := atomic.LoadInt32(&rl.connCount)

	// Chỉ sử dụng khóa đọc trên bản đồ giới hạn
	rl.mu.RLock()
	activeLimiters := len(rl.limiters)
	rl.mu.RUnlock()

	return map[string]interface{}{
		"enabled":             rl.enabled,
		"active_limiters":     activeLimiters,
		"current_connections": currentConns,
		"max_connections":     rl.maxConns,
		"requests_per_second": float64(rl.r),
		"burst_size":          rl.b,
	}
}
