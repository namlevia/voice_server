package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"voice_server/config"
	"voice_server/internal/bootstrap"
	"voice_server/internal/logger"
	"voice_server/internal/router"
)

func main() {

	if err := config.InitConfig("config.json"); err != nil {
		logger.Errorf("Tải cấu hình thất bại: %v", err)
		os.Exit(1)
	}

	logger.InitLoggerFromConfig(logger.LoggingConfig{
		Level:      config.GlobalConfig.Logging.Level,
		Format:     config.GlobalConfig.Logging.Format,
		Output:     config.GlobalConfig.Logging.Output,
		FilePath:   config.GlobalConfig.Logging.FilePath,
		MaxSize:    config.GlobalConfig.Logging.MaxSize,
		MaxBackups: config.GlobalConfig.Logging.MaxBackups,
		MaxAge:     config.GlobalConfig.Logging.MaxAge,
		Compress:   config.GlobalConfig.Logging.Compress,
	})
	logger.Infof("✅ Đã tải cấu hình")
	config.PrintConfig()

	deps, err := bootstrap.InitApp(&config.GlobalConfig)
	if err != nil {
		logger.Errorf("Khởi tạo phụ thuộc ứng dụng thất bại: %v", err)
		os.Exit(1)
	}

	r := router.NewRouter(deps)

	server := &http.Server{
		Addr:        fmt.Sprintf("%s:%d", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port),
		Handler:     deps.RateLimiter.Middleware(r),
		ReadTimeout: time.Duration(config.GlobalConfig.Server.ReadTimeout) * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		logger.Infof("🛑 Đang dừng máy chủ...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Errorf("Máy chủ buộc phải dừng: %v", err)
		}
		logger.Infof("✅ Máy chủ đã dừng hoàn tất")
	}()

	logger.Infof("🌐 Đang lắng nghe tại %s:%d", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)
	logger.Infof("🔗 WebSocket: ws://%s:%d/ws", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)
	logger.Infof("📊 Kiểm tra sức khỏe: http://%s:%d/health", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)
	logger.Infof("📈 Thống kê: http://%s:%d/stats", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)
	logger.Infof("🧪 Trang kiểm thử: http://%s:%d/", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Errorf("Lỗi máy chủ: %v", err)
		os.Exit(1)
	}
}
