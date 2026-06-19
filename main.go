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

	// Tải cấu hình
	if err := config.InitConfig("config.json"); err != nil {
		logger.Errorf("Failed to load configuration:%v", err)
		os.Exit(1)
	}

	// Đặt cấp độ nhật ký
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
	logger.Infof("✅ Configuration loaded")
	config.PrintConfig()

	// Khởi tạo tất cả các phụ thuộc
	deps, err := bootstrap.InitApp(&config.GlobalConfig)
	if err != nil {
		logger.Errorf("Failed to initialize app dependencies:%v", err)
		os.Exit(1)
	}

	// Đăng ký tất cả các tuyến đường thống nhất
	r := router.NewRouter(deps)

	// Tạo máy chủ HTTP
	server := &http.Server{
		Addr:        fmt.Sprintf("%s:%d", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port),
		Handler:     deps.RateLimiter.Middleware(r),
		ReadTimeout: time.Duration(config.GlobalConfig.Server.ReadTimeout) * time.Second,
	}

	// đóng cửa duyên dáng
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		logger.Infof("🛑 Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Errorf("Server forced to shutdown:%v", err)
		}
		logger.Infof("✅ Server shutdown complete")
	}()

	logger.Infof("🌐 Listening on %s:%d", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)
	logger.Infof("🔗 WebSocket: ws://%s:%d/ws", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)
	logger.Infof("📊 Health check: http://%s:%d/health", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)
	logger.Infof("📈 Statistics: http://%s:%d/stats", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)
	logger.Infof("🧪 Test page: http://%s:%d/", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Errorf("Server error:%v", err)
		os.Exit(1)
	}
}
