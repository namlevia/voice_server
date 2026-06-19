package hotreload

import (
	"fmt"
	"sync"
	"time"

	"voice_server/config"
	"voice_server/internal/logger"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// HotReloadManager định cấu hình trình quản lý tải lại nóng
type HotReloadManager struct {
	mu            sync.RWMutex
	callbacks     map[string][]func()
	watcher       *fsnotify.Watcher
	debounceTimer *time.Timer
	stopChan      chan struct{}
}

// NewHotReloadManager tạo trình quản lý tải lại nóng mới
func NewHotReloadManager() (*HotReloadManager, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	manager := &HotReloadManager{
		callbacks: make(map[string][]func()),
		watcher:   watcher,
		stopChan:  make(chan struct{}),
	}

	return manager, nil
}

// RegisterCallback Đăng ký gọi lại thay đổi cấu hình
func (m *HotReloadManager) RegisterCallback(configKey string, callback func()) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.callbacks[configKey] == nil {
		m.callbacks[configKey] = make([]func(), 0)
	}
	m.callbacks[configKey] = append(m.callbacks[configKey], callback)
}

// StartWatching bắt đầu giám sát tệp cấu hình
func (m *HotReloadManager) StartWatching(configPath string) error {
	// Thêm file cấu hình vào danh sách nghe
	if err := m.watcher.Add(configPath); err != nil {
		return fmt.Errorf("failed to watch config file: %w", err)
	}

	// Bắt đầu coroutine nghe
	go m.watchLoop()

	logger.Infof("🔍 Started watching config file: %s", configPath)
	return nil
}

// vòng lặp nghe watchLoop
func (m *HotReloadManager) watchLoop() {
	defer m.watcher.Close()

	for {
		select {
		case event := <-m.watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				m.handleConfigChange()
			}
		case err := <-m.watcher.Errors:
			logger.Errorf("❌ Config file watcher error: %v", err)
		case <-m.stopChan:
			logger.Infof("🛑 Config file watcher stopped")
			return
		}
	}
}

// handConfigChange xử lý các thay đổi tập tin cấu hình
func (m *HotReloadManager) handleConfigChange() {
	// Xử lý chống rung
	if m.debounceTimer != nil {
		m.debounceTimer.Stop()
	}

	m.debounceTimer = time.AfterFunc(2*time.Second, func() {
		m.reloadConfig()
	})
}

// tải lạiConfig cấu hình tải lại
func (m *HotReloadManager) reloadConfig() {
	logger.Infof("🔄 Reloading configuration...")

	// Đọc lại tập tin cấu hình
	if err := viper.ReadInConfig(); err != nil {
		logger.Errorf("❌ Failed to read config file: %v", err)
		return
	}

	// Cấu hình lại
	if err := viper.Unmarshal(&config.GlobalConfig); err != nil {
		logger.Errorf("❌ Failed to unmarshal config: %v", err)
		return
	}

	logger.Infof("✅ Configuration reloaded successfully")

	// Thực hiện chức năng gọi lại
	m.executeCallbacks()
}

// execCallbacks thực thi chức năng gọi lại
func (m *HotReloadManager) executeCallbacks() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for configKey, callbacks := range m.callbacks {
		logger.Infof("🔄 Executing callbacks for config key: %s", configKey)
		for _, callback := range callbacks {
			// Thực hiện các lệnh gọi lại trong goroutine để tránh bị chặn
			go func(cb func()) {
				defer func() {
					if r := recover(); r != nil {
						logger.Errorf("❌ Callback panicked: %v", r)
					}
				}()
				cb()
			}(callback)
		}
	}
}

// Dừng lại Ngừng nghe
func (m *HotReloadManager) Stop() {
	close(m.stopChan)
	if m.debounceTimer != nil {
		m.debounceTimer.Stop()
	}
}

// GetConfigValue Lấy giá trị cấu hình
func (m *HotReloadManager) GetConfigValue(key string) interface{} {
	return viper.Get(key)
}

// SetConfigValue đặt giá trị cấu hình
func (m *HotReloadManager) SetConfigValue(key string, value interface{}) error {
	viper.Set(key, value)

	// Phân tích lại cấu trúc
	if err := viper.Unmarshal(&config.GlobalConfig); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Thực hiện các cuộc gọi lại liên quan
	m.executeCallbacks()

	return nil
}

// SaveConfig lưu cấu hình vào một tập tin
func (m *HotReloadManager) SaveConfig() error {
	return viper.WriteConfig()
}
