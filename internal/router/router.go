package router

import (
	"voice_server/internal/bootstrap"
	"voice_server/internal/handlers"
	"voice_server/internal/ws"

	"github.com/gin-gonic/gin"
)

// NewRouter đăng ký tất cả các tuyến và trả về *gin.Engine
func NewRouter(deps *bootstrap.AppDependencies) *gin.Engine {
	ginRouter := gin.New()
	ginRouter.Use(gin.Recovery())
	// VIỆC CẦN LÀM: Tiêm gin.Logger() nếu cần

	// Đăng ký định tuyến cơ bản
	ginRouter.GET("/ws", func(c *gin.Context) {
		ws.HandleWebSocket(c.Writer, c.Request, deps.SessionManager, deps.GlobalRecognizer)
	})
	ginRouter.POST("/transcribe", handlers.TranscribeHandler(deps))
	ginRouter.GET("/health", handlers.HealthHandler(deps))
	ginRouter.GET("/stats", handlers.StatsHandler(deps))

	// Dịch vụ tập tin tĩnh
	ginRouter.Static("/static", "./static")
	ginRouter.StaticFile("/", "./static/index.html")

	// Đăng ký lộ trình nhận dạng giọng nói (nếu được bật)
	if deps.SpeakerHandler != nil {
		deps.SpeakerHandler.RegisterRoutes(ginRouter)
	}

	return ginRouter
}
