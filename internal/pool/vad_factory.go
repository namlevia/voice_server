package pool

import (
	"fmt"

	"voice_server/config"
	"voice_server/internal/logger"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Nhà máy VADFactory Nhà máy VAD
type VADFactory struct {
	factories map[string]VADPoolFactory
}

// NewVADFactory thành lập nhà máy VAD mới
func NewVADFactory() *VADFactory {
	factory := &VADFactory{
		factories: make(map[string]VADPoolFactory),
	}

	// Đăng ký các loại VAD được hỗ trợ
	factory.RegisterFactory(SILERO_TYPE, &SileroVADPoolFactory{})
	factory.RegisterFactory(TEN_VAD_TYPE, &TenVADPoolFactory{})

	return factory
}

// RegisterFactory đăng ký nhà máy sản xuất bể bơi VAD
func (f *VADFactory) RegisterFactory(vadType string, factory VADPoolFactory) {
	f.factories[vadType] = factory
	logger.Infof("🔧 Registered VAD factory for type: %s", vadType)
}

// CreateVADPool tạo nhóm VAD dựa trên cấu hình
func (f *VADFactory) CreateVADPool() (VADPoolInterface, error) {
	vadType := config.GlobalConfig.VAD.Provider

	logger.Infof("🔧 Creating VAD pool with type: %s", vadType)

	factory, exists := f.factories[vadType]
	if !exists {
		return nil, fmt.Errorf("unsupported VAD type: %s", vadType)
	}

	// Tạo cấu hình dựa trên loại VAD
	var config interface{}
	var err error

	switch vadType {
	case SILERO_TYPE:
		config, err = f.createSileroConfig()
	case TEN_VAD_TYPE:
		config, err = f.createTenVADConfig()
	default:
		return nil, fmt.Errorf("unsupported VAD type: %s", vadType)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create config for %s: %v", vadType, err)
	}

	// Tạo một hồ bơi bằng cách sử dụng một nhà máy
	pool, err := factory.CreatePool(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s VAD pool: %v", vadType, err)
	}

	return pool, nil
}

// createSileroConfig tạo cấu hình Silero VAD
func (f *VADFactory) createSileroConfig() (*SileroVADConfig, error) {
	// Tạo cấu hình VAD
	vadConfig := &sherpa.VadModelConfig{
		SileroVad: sherpa.SileroVadModelConfig{
			Model:              config.GlobalConfig.VAD.SileroVAD.ModelPath,
			Threshold:          config.GlobalConfig.VAD.SileroVAD.Threshold,
			MinSilenceDuration: config.GlobalConfig.VAD.SileroVAD.MinSilenceDuration,
			MinSpeechDuration:  config.GlobalConfig.VAD.SileroVAD.MinSpeechDuration,
			WindowSize:         config.GlobalConfig.VAD.SileroVAD.WindowSize,
			MaxSpeechDuration:  config.GlobalConfig.VAD.SileroVAD.MaxSpeechDuration,
		},
		SampleRate: config.GlobalConfig.Audio.SampleRate,
		NumThreads: config.GlobalConfig.Recognition.NumThreads,
		Provider:   config.GlobalConfig.Recognition.Provider,
		Debug:      0,
	}

	return &SileroVADConfig{
		ModelConfig:       vadConfig,
		BufferSizeSeconds: config.GlobalConfig.VAD.SileroVAD.BufferSizeSeconds,
		PoolSize:          config.GlobalConfig.VAD.PoolSize,
		MaxIdle:           0, // MaxIdle hiện không được hỗ trợ
	}, nil
}

// createTenVADConfig tạo cấu hình TEN-VAD
func (f *VADFactory) createTenVADConfig() (*TenVADConfig, error) {
	return &TenVADConfig{
		HopSize:   config.GlobalConfig.VAD.TenVAD.HopSize,
		Threshold: config.GlobalConfig.VAD.Threshold,
		PoolSize:  config.GlobalConfig.VAD.PoolSize,
		MaxIdle:   0, // MaxIdle hiện không được hỗ trợ
	}, nil
}

// GetVADType Lấy loại VAD hiện tại
func (f *VADFactory) GetVADType() string {
	return config.GlobalConfig.VAD.Provider
}

// GetSupportedTypes Nhận các loại VAD được hỗ trợ
func (f *VADFactory) GetSupportedTypes() []string {
	types := make([]string, 0, len(f.factories))
	for vadType := range f.factories {
		types = append(types, vadType)
	}
	return types
}

// SileroVADPoolNhà máy sản xuất bể bơi Silero VAD
type SileroVADPoolFactory struct{}

// CreatePool tạo nhóm Silero VAD
func (f *SileroVADPoolFactory) CreatePool(config interface{}) (VADPoolInterface, error) {
	sileroConfig, ok := config.(*SileroVADConfig)
	if !ok {
		return nil, fmt.Errorf("invalid config type for Silero VAD")
	}

	pool := NewSileroVADPool(sileroConfig)
	return pool, nil
}

// GetSupportedTypes Nhận các loại VAD được hỗ trợ
func (f *SileroVADPoolFactory) GetSupportedTypes() []string {
	return []string{SILERO_TYPE}
}

// TenVADPoolFactory Nhà máy bể bơi TEN-VAD
type TenVADPoolFactory struct{}

// CreatePool tạo nhóm TEN-VAD
func (f *TenVADPoolFactory) CreatePool(config interface{}) (VADPoolInterface, error) {
	tenVADConfig, ok := config.(*TenVADConfig)
	if !ok {
		return nil, fmt.Errorf("invalid config type for TEN-VAD")
	}

	pool := NewTenVADPool(tenVADConfig)
	return pool, nil
}

// GetSupportedTypes Nhận các loại VAD được hỗ trợ
func (f *TenVADPoolFactory) GetSupportedTypes() []string {
	return []string{TEN_VAD_TYPE}
}
