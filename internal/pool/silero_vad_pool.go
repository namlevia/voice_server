package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"voice_server/internal/logger"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Cấu hình SileroVADConfig Cấu hình Silero VAD
type SileroVADConfig struct {
	ModelConfig       *sherpa.VadModelConfig
	BufferSizeSeconds float32
	PoolSize          int
	MaxIdle           int
}

// Phiên bản SileroVADInstance Silero VAD
type SileroVADInstance struct {
	ID       int
	VAD      *sherpa.VoiceActivityDetector
	LastUsed int64
	InUse    int32
	mu       sync.RWMutex
}

// GetID Lấy ID phiên bản
func (i *SileroVADInstance) GetID() int {
	return i.ID
}

// GetType lấy loại VAD
func (i *SileroVADInstance) GetType() string {
	return SILERO_TYPE
}

// IsInUse kiểm tra xem nó có được sử dụng không
func (i *SileroVADInstance) IsInUse() bool {
	return atomic.LoadInt32(&i.InUse) == 1
}

// SetInUse đặt trạng thái sử dụng
func (i *SileroVADInstance) SetInUse(inUse bool) {
	if inUse {
		atomic.StoreInt32(&i.InUse, 1)
	} else {
		atomic.StoreInt32(&i.InUse, 0)
	}
}

// GetLastUsed Lấy thời gian sử dụng cuối cùng
func (i *SileroVADInstance) GetLastUsed() int64 {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.LastUsed
}

// SetLastUsed đặt thời gian sử dụng cuối cùng
func (i *SileroVADInstance) SetLastUsed(timestamp int64) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.LastUsed = timestamp
}

// Đặt lại trạng thái đặt lại phiên bản
func (i *SileroVADInstance) Reset() error {
	if i.VAD != nil {
		// Xóa bộ đệm Silero VAD
		for !i.VAD.IsEmpty() {
			segment := i.VAD.Front()
			i.VAD.Pop()
			if segment != nil {
				// Phát hành tài nguyên phân khúc (nếu cần)
			}
		}
	}
	return nil
}

// Phá hủy phiên bản
func (i *SileroVADInstance) Destroy() error {
	if i.VAD != nil {
		sherpa.DeleteVoiceActivityDetector(i.VAD)
		i.VAD = nil
		logger.Infof("🗑️ Silero VAD instance destroyed")
	}
	return nil
}

// SileroVADPool Nhóm tài nguyên Silero VAD
type SileroVADPool struct {
	instances []*SileroVADInstance
	available chan VADInstanceInterface
	config    *SileroVADConfig

	// Thống kê
	totalCreated int64
	totalReused  int64
	totalActive  int64

	// điều khiển
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
}

// NewSileroVADPool tạo nhóm tài nguyên Silero VAD mới
func NewSileroVADPool(config *SileroVADConfig) *SileroVADPool {
	ctx, cancel := context.WithCancel(context.Background())

	pool := &SileroVADPool{
		instances: make([]*SileroVADInstance, 0, config.PoolSize),
		available: make(chan VADInstanceInterface, config.PoolSize),
		config:    config,
		ctx:       ctx,
		cancel:    cancel,
	}

	return pool
}

// Khởi tạo khởi tạo nhóm VAD song song
func (p *SileroVADPool) Initialize() error {
	logger.Infof("🔧 Initializing Silero VAD pool with %d instances...", p.config.PoolSize)

	// Khởi tạo song song các phiên bản VAD
	var initWg sync.WaitGroup
	errorChan := make(chan error, p.config.PoolSize)

	for i := 0; i < p.config.PoolSize; i++ {
		initWg.Add(1)
		go func(instanceID int) {
			defer initWg.Done()

			// Tạo phiên bản VAD
			vad := sherpa.NewVoiceActivityDetector(p.config.ModelConfig, p.config.BufferSizeSeconds)
			if vad == nil {
				errorChan <- fmt.Errorf("failed to create Silero VAD instance %d", instanceID)
				return
			}

			instance := &SileroVADInstance{
				VAD:      vad,
				LastUsed: time.Now().UnixNano(),
				InUse:    0,
				ID:       instanceID,
			}

			p.mu.Lock()
			p.instances = append(p.instances, instance)
			p.mu.Unlock()

			// đưa vào hàng đợi có sẵn
			select {
			case p.available <- instance:
				atomic.AddInt64(&p.totalCreated, 1)
				logger.Infof("✅ Silero VAD instance %d initialized", instanceID)
			default:
				// Hàng đợi đã đầy và phiên bản bị hủy
				sherpa.DeleteVoiceActivityDetector(vad)
				errorChan <- fmt.Errorf("Silero VAD pool queue full, instance %d discarded", instanceID)
			}
		}(i)
	}

	initWg.Wait()
	close(errorChan)

	// Kiểm tra lỗi khởi tạo
	var initErrors []error
	for err := range errorChan {
		if err != nil {
			initErrors = append(initErrors, err)
			logger.Warnf("⚠️ Silero VAD initialization warning: %v", err)
		}
	}

	successCount := len(p.instances)
	logger.Infof("🚀 Silero VAD pool initialized with %d/%d instances", successCount, p.config.PoolSize)

	if len(initErrors) > 0 && successCount == 0 {
		return fmt.Errorf("failed to initialize any Silero VAD instances")
	}

	return nil
}

// Nhận phiên bản VAD
func (p *SileroVADPool) Get() (VADInstanceInterface, error) {
	logger.Infof("🔍 Attempting to get Silero VAD instance from pool (available: %d)", len(p.available))

	select {
	case instance := <-p.available:
		logger.Infof("🎯 Got Silero VAD instance %d from pool", instance.GetID())
		if atomic.CompareAndSwapInt32(&instance.(*SileroVADInstance).InUse, 0, 1) {
			instance.SetLastUsed(time.Now().UnixNano())
			atomic.AddInt64(&p.totalReused, 1)
			atomic.AddInt64(&p.totalActive, 1)
			logger.Infof("✅ Silero VAD instance %d marked as in-use (active: %d)", instance.GetID(), atomic.LoadInt64(&p.totalActive))
			return instance, nil
		}
		// Phiên bản đã được sử dụng và được đưa trở lại hàng đợi.
		logger.Warnf("⚠️ Silero VAD instance %d already in use, returning to pool", instance.GetID())
		select {
		case p.available <- instance:
		default:
		}
		return p.Get() // thử lại đệ quy
	case <-time.After(100 * time.Millisecond):
		// Hết thời gian chờ, tạo phiên bản mới
		logger.Warnf("⏰ Silero VAD pool timeout, creating new temporary instance")
		return p.createNewInstance()
	case <-p.ctx.Done():
		logger.Errorf("❌ Silero VAD pool is shutting down")
		return nil, fmt.Errorf("Silero VAD pool is shutting down")
	}
}

// Put trả về phiên bản VAD
func (p *SileroVADPool) Put(instance VADInstanceInterface) {
	if instance == nil {
		logger.Warnf("⚠️ Attempted to put nil Silero VAD instance")
		return
	}

	logger.Infof("🔄 Returning Silero VAD instance %d to pool", instance.GetID())

	if atomic.CompareAndSwapInt32(&instance.(*SileroVADInstance).InUse, 1, 0) {
		instance.SetLastUsed(time.Now().UnixNano())
		atomic.AddInt64(&p.totalActive, -1)
		logger.Infof("✅ Silero VAD instance %d marked as available (active: %d)", instance.GetID(), atomic.LoadInt64(&p.totalActive))

		// Đặt lại trạng thái VAD
		if err := instance.Reset(); err != nil {
			logger.Warnf("⚠️ Failed to reset Silero VAD instance %d: %v", instance.GetID(), err)
		}

		select {
		case p.available <- instance:
			// Đã trả lại thành công
			logger.Infof("✅ Silero VAD instance %d returned to pool (available: %d)", instance.GetID(), len(p.available))
		default:
			// Hàng đợi đã đầy và phiên bản bị hủy
			logger.Warnf("⚠️ Silero VAD pool queue full, destroying instance %d", instance.GetID())
			instance.Destroy()
		}
	} else {
		logger.Warnf("⚠️ Silero VAD instance %d was not in use, cannot return", instance.GetID())
	}
}

// createNewInstance tạo một phiên bản VAD mới
func (p *SileroVADPool) createNewInstance() (VADInstanceInterface, error) {
	vad := sherpa.NewVoiceActivityDetector(p.config.ModelConfig, p.config.BufferSizeSeconds)
	if vad == nil {
		return nil, fmt.Errorf("failed to create new Silero VAD instance")
	}

	instance := &SileroVADInstance{
		VAD:      vad,
		LastUsed: time.Now().UnixNano(),
		InUse:    1,
		ID:       -1, // ví dụ tạm thời
	}

	atomic.AddInt64(&p.totalCreated, 1)
	atomic.AddInt64(&p.totalActive, 1)

	logger.Infof("🆕 Created temporary Silero VAD instance")
	return instance, nil
}

// GetStatsNhận số liệu thống kê
func (p *SileroVADPool) GetStats() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return map[string]interface{}{
		"vad_type":        SILERO_TYPE,
		"pool_size":       p.config.PoolSize,
		"max_idle":        p.config.MaxIdle,
		"total_instances": len(p.instances),
		"available_count": len(p.available),
		"active_count":    atomic.LoadInt64(&p.totalActive),
		"total_created":   atomic.LoadInt64(&p.totalCreated),
		"total_reused":    atomic.LoadInt64(&p.totalReused),
	}
}

// Tắt máy sẽ đóng nhóm VAD
func (p *SileroVADPool) Shutdown() {
	logger.Infof("🛑 Shutting down Silero VAD pool...")

	// Hủy ngữ cảnh
	p.cancel()

	// Phá hủy tất cả các trường hợp
	p.mu.Lock()
	defer p.mu.Unlock()

	// Xóa hàng đợi có sẵn
	for {
		select {
		case instance := <-p.available:
			instance.Destroy()
		default:
			goto cleanup_instances
		}
	}

cleanup_instances:
	// Phá hủy tất cả các trường hợp
	for _, instance := range p.instances {
		instance.Destroy()
	}

	p.instances = nil
	close(p.available)

	logger.Infof("✅ Silero VAD pool shutdown complete")
}
