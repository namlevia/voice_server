package handlers

import (
	"voice_server/internal/bootstrap"
	"time"

	"github.com/gin-gonic/gin"
)

// Giao diện thống kê StatsHandler (tiêm phụ thuộc)
func StatsHandler(deps *bootstrap.AppDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		stats := map[string]interface{}{
			"timestamp": time.Now().Format(time.RFC3339),
		}
		if deps.VADPool != nil {
			stats["vad_pool"] = deps.VADPool.GetStats()
		}
		if deps.SessionManager != nil {
			stats["sessions"] = deps.SessionManager.GetStats()
		}
		if deps.RateLimiter != nil {
			stats["rate_limit"] = deps.RateLimiter.GetStats()
		}
		c.JSON(200, stats)
	}
}
