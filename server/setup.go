// Máy chủ gói cung cấp một điểm vào để chương trình chính nhúng asr_server nhằm ngăn chương trình chính tham chiếu trực tiếp đến gói nội bộ.
package server

import (
	"fmt"
	"net/http"
	"time"

	"voice_server/config"
	"voice_server/internal/bootstrap"
	"voice_server/internal/logger"
	"voice_server/internal/router"
)

// Thiết lập tải cấu hình, khởi tạo phần phụ thuộc asr_server và trả về Trình xử lý HTTP cũng như địa chỉ nghe để sử dụng khi nhúng quy trình chính.
// Trả về: trình xử lý, addr (chẳng hạn như "0.0.0.0:8080"), readTimeout, lỗi
func Setup(configPath string) (http.Handler, string, time.Duration, error) {
	if err := config.InitConfig(configPath); err != nil {
		return nil, "", 0, err
	}
	cfg := config.GetConfig()

	logger.InitLoggerFromConfig(logger.LoggingConfig{
		Level:      cfg.Logging.Level,
		Format:     cfg.Logging.Format,
		Output:     cfg.Logging.Output,
		FilePath:   cfg.Logging.FilePath,
		MaxSize:    cfg.Logging.MaxSize,
		MaxBackups: cfg.Logging.MaxBackups,
		MaxAge:     cfg.Logging.MaxAge,
		Compress:   cfg.Logging.Compress,
	})

	deps, err := bootstrap.InitApp(cfg)
	if err != nil {
		return nil, "", 0, err
	}

	r := router.NewRouter(deps)
	handler := deps.RateLimiter.Middleware(r)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	readTimeout := time.Duration(cfg.Server.ReadTimeout) * time.Second
	if readTimeout <= 0 {
		readTimeout = 30 * time.Second
	}
	return handler, addr, readTimeout, nil
}
