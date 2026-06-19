package bootstrap

import (
	"fmt"
	"os"
	"strconv"

	"voice_server/config"
	"voice_server/internal/config/hotreload"
	"voice_server/internal/logger"
	"voice_server/internal/middleware"
	"voice_server/internal/pool"
	"voice_server/internal/session"
	"voice_server/internal/speaker"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

type AppDependencies struct {
	SessionManager   *session.Manager
	VADPool          pool.VADPoolInterface
	RateLimiter      *middleware.RateLimiter
	SpeakerManager   *speaker.Manager
	SpeakerHandler   *speaker.Handler
	GlobalRecognizer *sherpa.OfflineRecognizer
	HotReloadMgr     *hotreload.HotReloadManager
}

// createRecognizer được sử dụng để khởi tạo trình nhận dạng sherpa
func createRecognizer(cfg *config.Config) (*sherpa.OfflineRecognizer, error) {
	c := sherpa.OfflineRecognizerConfig{}
	c.FeatConfig.SampleRate = cfg.Audio.SampleRate
	c.FeatConfig.FeatureDim = cfg.Audio.FeatureDim

	if cfg.Recognition.ModelType == "transducer" {
		c.ModelConfig.Transducer.Encoder = cfg.Recognition.EncoderPath
		c.ModelConfig.Transducer.Decoder = cfg.Recognition.DecoderPath
		c.ModelConfig.Transducer.Joiner = cfg.Recognition.JoinerPath
	} else {
		// Default to SenseVoice if not specified or specified as sense_voice
		c.ModelConfig.SenseVoice.Model = cfg.Recognition.ModelPath
	}
	
	c.ModelConfig.Tokens = cfg.Recognition.TokensPath
	c.ModelConfig.NumThreads = cfg.Recognition.NumThreads
	c.ModelConfig.Debug = 0
	if cfg.Recognition.Debug {
		c.ModelConfig.Debug = 1
	}
	c.ModelConfig.Provider = cfg.Recognition.Provider

	recognizer := sherpa.NewOfflineRecognizer(&c)
	if recognizer == nil {
		return nil, fmt.Errorf("failed to create offline recognizer")
	}

	return recognizer, nil
}

// registerHotReloadCallbacks đăng ký cấu hình các cuộc gọi lại tải lại nóng
func registerHotReloadCallbacks(hotReloadMgr *hotreload.HotReloadManager) {
	if hotReloadMgr == nil {
		return
	}

	hotReloadMgr.RegisterCallback("logging.level", func() {
		logger.Infof("🔄 Log level changed to: %s", config.GlobalConfig.Logging.Level)
	})
	hotReloadMgr.RegisterCallback("vad", func() {
		logger.Infof("🔄 VAD configuration changed")
	})
	hotReloadMgr.RegisterCallback("session", func() {
		logger.Infof("🔄 Session configuration changed")
	})
	hotReloadMgr.RegisterCallback("rate_limit", func() {
		logger.Infof("🔄 Rate limit configuration changed")
	})
	hotReloadMgr.RegisterCallback("response", func() {
		logger.Infof("🔄 Response configuration changed")
	})
	logger.Infof("✅ Hot reload callbacks registered")
}

// InitApp khởi tạo tất cả các thành phần cốt lõi và trả về cấu trúc chèn phần phụ thuộc
func InitApp(cfg *config.Config) (*AppDependencies, error) {
	logger.Infof("🔧 Initializing components...")

	// Khởi tạo cấu hình trình quản lý tải lại nóng
	logger.Infof("🔧 Initializing hot reload manager...")
	hotReloadMgr, err := hotreload.NewHotReloadManager()
	if err != nil {
		logger.Errorf("Failed to initialize hot reload manager: %v", err)
		return nil, fmt.Errorf("failed to initialize hot reload manager: %v", err)
	}
	if err := hotReloadMgr.StartWatching("config.json"); err != nil {
		logger.Warnf("Failed to start config file watching, continuing without hot reload: %v", err)
	}

	// Khởi tạo trình nhận dạng chung (chỉ khởi tạo khi bật tính năng nhận dạng)
	var globalRecognizer *sherpa.OfflineRecognizer
	if cfg.Recognition.Enabled {
		// Khởi tạo trình nhận dạng toàn cầu
		logger.Infof("🔧 Initializing global recognizer...")
		globalRecognizer, err = createRecognizer(cfg)
		if err != nil {
			logger.Errorf("Failed to initialize global recognizer: %v", err)
			return nil, fmt.Errorf("failed to initialize global recognizer: %v", err)
		}
	}

	// Khởi tạo nhóm VAD (luôn được khởi tạo, không dựa vào nhận dạng.enabled)
	var vadPool pool.VADPoolInterface
	logger.Infof("🔧 Initializing VAD pool...")
	if cfg.VAD.Provider != "none" {
		vadFactory := pool.NewVADFactory()

		if config.GlobalConfig.VAD.Provider == pool.SILERO_TYPE {
			// Kiểm tra xem tệp mô hình VAD có tồn tại không (chỉ bắt buộc đối với silero)
			if _, err := os.Stat(cfg.VAD.SileroVAD.ModelPath); os.IsNotExist(err) {
				logger.Errorf("VAD model file not found, model_path=%s", cfg.VAD.SileroVAD.ModelPath)
				return nil, fmt.Errorf("VAD model file not found: %s", cfg.VAD.SileroVAD.ModelPath)
			}
		}

		// Tạo nhóm VAD bằng cách sử dụng nhà máy
		vadPool, err = vadFactory.CreateVADPool()
		if err != nil {
			logger.Errorf("Failed to create VAD pool: %v", err)
			return nil, fmt.Errorf("failed to create VAD pool: %v", err)
		}

		// Khởi tạo nhóm VAD
		logger.Infof("🔧 Initializing VAD pool... pool_size=%d", cfg.VAD.PoolSize)
		if err := vadPool.Initialize(); err != nil {
			logger.Errorf("Failed to initialize VAD pool: %v", err)
			return nil, fmt.Errorf("failed to initialize VAD pool: %v", err)
		}
	} else {
		logger.Infof("🔧 VAD is disabled (provider=none), skipping VAD pool initialization")
	}

	// Khởi tạo trình quản lý phiên
	logger.Infof("🔧 Initializing session manager...")
	sessionManager := session.NewManager(globalRecognizer, vadPool)

	// Đăng ký cấu hình gọi lại tải lại nóng
	registerHotReloadCallbacks(hotReloadMgr)

	// Khởi tạo giới hạn tốc độ
	logger.Infof("🔧 Initializing rate limiter... requests_per_second=%d, max_connections=%d", cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.MaxConnections)
	rateLimiter := middleware.NewRateLimiter(
		cfg.RateLimit.Enabled,
		cfg.RateLimit.RequestsPerSecond,
		cfg.RateLimit.BurstSize,
		cfg.RateLimit.MaxConnections,
	)

	// Khởi tạo mô-đun nhận dạng giọng nói
	var speakerManager *speaker.Manager
	var speakerHandler *speaker.Handler
	if cfg.Speaker.Enabled {
		if _, statErr := os.Stat(cfg.Speaker.ModelPath); !os.IsNotExist(statErr) {
			speakerConfig := &speaker.Config{
				ModelPath:   cfg.Speaker.ModelPath,
				NumThreads:  cfg.Speaker.NumThreads,
				Provider:    cfg.Speaker.Provider,
				Threshold:   cfg.Speaker.Threshold,
				DataDir:     cfg.Speaker.DataDir,
				StorageType: cfg.Speaker.StorageType,
			}
			speakerConfig.JSONStorage.FilePath = cfg.Speaker.JSONStorage.FilePath
			// Đặt cấu hình cơ sở dữ liệu vectơ Qdrant (đọc từ các biến môi trường trước, sau đó đọc từ tệp cấu hình)
			// Đặt tên biến môi trường: QDRANT_HOST, QDRANT_PORT, QDRANT_COLLECTION_NAME
			if envHost := os.Getenv("QDRANT_HOST"); envHost != "" {
				speakerConfig.Qdrant.Host = envHost
				logger.Infof("Using Qdrant host from environment variable: %s", envHost)
			} else {
				speakerConfig.Qdrant.Host = cfg.Speaker.Qdrant.Host
			}

			if envPort := os.Getenv("QDRANT_PORT"); envPort != "" {
				if port, err := strconv.Atoi(envPort); err == nil {
					speakerConfig.Qdrant.Port = port
					logger.Infof("Using Qdrant port from environment variable: %d", port)
				} else {
					logger.Warnf("Invalid QDRANT_PORT environment variable: %s, using config file value", envPort)
					speakerConfig.Qdrant.Port = cfg.Speaker.Qdrant.Port
				}
			} else {
				speakerConfig.Qdrant.Port = cfg.Speaker.Qdrant.Port
			}

			if envCollectionName := os.Getenv("QDRANT_COLLECTION_NAME"); envCollectionName != "" {
				speakerConfig.Qdrant.CollectionName = envCollectionName
				logger.Infof("Using Qdrant collection name from environment variable: %s", envCollectionName)
			} else {
				speakerConfig.Qdrant.CollectionName = cfg.Speaker.Qdrant.CollectionName
			}

			mgr, err := speaker.NewManager(speakerConfig, vadPool)
			if err == nil {
				speakerManager = mgr
				speakerHandler = speaker.NewHandler(speakerManager)
			} else {
				logger.Warnf("Failed to initialize speaker recognition module, continuing without it: %v", err)
			}
		} else {
			logger.Warnf("Speaker model file not found, speaker recognition disabled, model_path=%s", cfg.Speaker.ModelPath)
		}
	}

	logger.Infof("✅ All components initialized successfully")
	return &AppDependencies{
		SessionManager:   sessionManager,
		VADPool:          vadPool,
		RateLimiter:      rateLimiter,
		SpeakerManager:   speakerManager,
		SpeakerHandler:   speakerHandler,
		GlobalRecognizer: globalRecognizer,
		HotReloadMgr:     hotReloadMgr,
	}, nil
}
