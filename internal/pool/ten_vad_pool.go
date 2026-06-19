package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"voice_server/internal/logger"
)

// Cấu hình TenVADConfig TEN-VAD
type TenVADConfig struct {
	HopSize   int
	Threshold float32
	PoolSize  int
	MaxIdle   int
}

// Phiên bản TenVADInstance TEN-VAD
type TenVADInstance struct {
	ID       int
	Handle   unsafe.Pointer
	LastUsed int64
	InUse    int32
	mu       sync.RWMutex
}

// GetID Lấy ID phiên bản
func (i *TenVADInstance) GetID() int {
	return i.ID
}

// GetType lấy loại VAD
func (i *TenVADInstance) GetType() string {
	return TEN_VAD_TYPE
}

// IsInUse kiểm tra xem nó có được sử dụng không
func (i *TenVADInstance) IsInUse() bool {
	return atomic.LoadInt32(&i.InUse) == 1
}

// SetInUse đặt trạng thái sử dụng
func (i *TenVADInstance) SetInUse(inUse bool) {
	if inUse {
		atomic.StoreInt32(&i.InUse, 1)
	} else {
		atomic.StoreInt32(&i.InUse, 0)
	}
}

// GetLastUsed Lấy thời gian sử dụng cuối cùng
func (i *TenVADInstance) GetLastUsed() int64 {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.LastUsed
}

// SetLastUsed đặt thời gian sử dụng cuối cùng
func (i *TenVADInstance) SetLastUsed(timestamp int64) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.LastUsed = timestamp
}

// Đặt lại trạng thái đặt lại phiên bản
func (i *TenVADInstance) Reset() error {
	// TEN-VAD không cần thiết lập lại, mỗi quá trình xử lý đều độc lập
	return nil
}

// Phá hủy phiên bản
func (i *TenVADInstance) Destroy() error {
	if i.Handle != nil {
		tenVAD := GetInstance()
		tenVAD.DestroyInstance(i.Handle)
		i.Handle = nil
		logger.Infof("🗑️ TEN-VAD instance %d destroyed", i.ID)
	}
	return nil
}

// Nhóm tài nguyên TenVADPool TEN-VAD
type TenVADPool struct {
	instances []*TenVADInstance
	available chan VADInstanceInterface
	config    *TenVADConfig

	// Thống kê
	totalCreated int64
	totalReused  int64
	totalActive  int64

	// điều khiển
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
}

// NewTenVADPool tạo nhóm tài nguyên TEN-VAD mới
func NewTenVADPool(config *TenVADConfig) *TenVADPool {
	ctx, cancel := context.WithCancel(context.Background())

	pool := &TenVADPool{
		instances: make([]*TenVADInstance, 0, config.PoolSize),
		available: make(chan VADInstanceInterface, config.PoolSize),
		config:    config,
		ctx:       ctx,
		cancel:    cancel,
	}

	return pool
}

// Khởi tạo khởi tạo nhóm VAD song song
func (p *TenVADPool) Initialize() error {
	logger.Infof("🔧 Initializing TEN-VAD pool with %d instances...", p.config.PoolSize)

	// Khởi tạo song song các phiên bản VAD
	var initWg sync.WaitGroup
	errorChan := make(chan error, p.config.PoolSize)

	for i := 0; i < p.config.PoolSize; i++ {
		initWg.Add(1)
		go func(instanceID int) {
			defer initWg.Done()

			// Tạo phiên bản TEN-VAD
			tenVAD := GetInstance()
			handle, err := tenVAD.CreateInstance(p.config.HopSize, p.config.Threshold)
			if err != nil {
				errorChan <- fmt.Errorf("failed to create TEN-VAD instance %d: %v", instanceID, err)
				return
			}

			instance := &TenVADInstance{
				Handle:   handle,
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
				logger.Infof("✅ TEN-VAD instance %d initialized", instanceID)
			default:
				// Hàng đợi đã đầy và phiên bản bị hủy
				tenVAD.DestroyInstance(handle)
				errorChan <- fmt.Errorf("TEN-VAD pool queue full, instance %d discarded", instanceID)
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
			logger.Warnf("⚠️ TEN-VAD initialization warning: %v", err)
		}
	}

	successCount := len(p.instances)
	logger.Infof("🚀 TEN-VAD pool initialized with %d/%d instances", successCount, p.config.PoolSize)

	if len(initErrors) > 0 && successCount == 0 {
		return fmt.Errorf("failed to initialize any TEN-VAD instances")
	}

	return nil
}

// Nhận phiên bản VAD
func (p *TenVADPool) Get() (VADInstanceInterface, error) {
	logger.Infof("🔍 Attempting to get TEN-VAD instance from pool (available: %d)", len(p.available))

	select {
	case instance := <-p.available:
		logger.Infof("🎯 Got TEN-VAD instance %d from pool", instance.GetID())
		if atomic.CompareAndSwapInt32(&instance.(*TenVADInstance).InUse, 0, 1) {
			instance.SetLastUsed(time.Now().UnixNano())
			atomic.AddInt64(&p.totalReused, 1)
			atomic.AddInt64(&p.totalActive, 1)
			logger.Infof("✅ TEN-VAD instance %d marked as in-use (active: %d)", instance.GetID(), atomic.LoadInt64(&p.totalActive))
			return instance, nil
		}
		// Phiên bản đã được sử dụng và được đưa trở lại hàng đợi.
		logger.Warnf("⚠️ TEN-VAD instance %d already in use, returning to pool", instance.GetID())
		select {
		case p.available <- instance:
		default:
		}
		return p.Get() // thử lại đệ quy
	case <-time.After(100 * time.Millisecond):
		// Hết thời gian chờ, tạo phiên bản mới
		logger.Warnf("⏰ TEN-VAD pool timeout, creating new temporary instance")
		return p.createNewInstance()
	case <-p.ctx.Done():
		logger.Error("❌ TEN-VAD pool is shutting down")
		return nil, fmt.Errorf("TEN-VAD pool is shutting down")
	}
}

// Put trả về phiên bản VAD
func (p *TenVADPool) Put(instance VADInstanceInterface) {
	if instance == nil {
		logger.Warnf("⚠️ Attempted to put nil TEN-VAD instance")
		return
	}

	logger.Infof("🔄 Returning TEN-VAD instance %d to pool", instance.GetID())

	if atomic.CompareAndSwapInt32(&instance.(*TenVADInstance).InUse, 1, 0) {
		instance.SetLastUsed(time.Now().UnixNano())
		atomic.AddInt64(&p.totalActive, -1)
		logger.Infof("✅ TEN-VAD instance %d marked as available (active: %d)", instance.GetID(), atomic.LoadInt64(&p.totalActive))

		// Đặt lại trạng thái VAD
		if err := instance.Reset(); err != nil {
			logger.Warnf("⚠️ Failed to reset TEN-VAD instance %d: %v", instance.GetID(), err)
		}

		select {
		case p.available <- instance:
			// Đã trả lại thành công
			logger.Infof("✅ TEN-VAD instance %d returned to pool (available: %d)", instance.GetID(), len(p.available))
		default:
			// Hàng đợi đã đầy và phiên bản bị hủy
			logger.Warnf("⚠️ TEN-VAD pool queue full, destroying instance %d", instance.GetID())
			instance.Destroy()
		}
	} else {
		logger.Warnf("⚠️ TEN-VAD instance %d was not in use, cannot return", instance.GetID())
	}
}

// createNewInstance tạo một phiên bản VAD mới
func (p *TenVADPool) createNewInstance() (VADInstanceInterface, error) {
	tenVAD := GetInstance()
	handle, err := tenVAD.CreateInstance(p.config.HopSize, p.config.Threshold)
	if err != nil {
		return nil, fmt.Errorf("failed to create new TEN-VAD instance: %v", err)
	}

	instance := &TenVADInstance{
		Handle:   handle,
		LastUsed: time.Now().UnixNano(),
		InUse:    1,
		ID:       -1, // ví dụ tạm thời
	}

	atomic.AddInt64(&p.totalCreated, 1)
	atomic.AddInt64(&p.totalActive, 1)

	logger.Infof("🆕 Created temporary TEN-VAD instance")
	return instance, nil
}

// GetStatsNhận số liệu thống kê
func (p *TenVADPool) GetStats() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return map[string]interface{}{
		"vad_type":        TEN_VAD_TYPE,
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
func (p *TenVADPool) Shutdown() {
	logger.Infof("🛑 Shutting down TEN-VAD pool...")

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

	logger.Infof("✅ TEN-VAD pool shutdown complete")
}
