package speaker

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"voice_server/config"
	"voice_server/internal/logger"

	"github.com/gin-gonic/gin"
	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/gorilla/websocket"
)

// Trình xử lý HTTP nhận dạng giọng nói
type Handler struct {
	manager *Manager
}

// NewHandler tạo một trình xử lý mới
func NewHandler(manager *Manager) *Handler {
	return &Handler{
		manager: manager,
	}
}

// getUIDFromRequest Trích xuất UID từ yêu cầu
// Ưu tiên: Tiêu đề yêu cầu X-User-ID > Uid tham số truy vấn > uid trường biểu mẫu
func getUIDFromRequest(c *gin.Context) string {
	// 1. Lấy từ tiêu đề yêu cầu
	if uid := c.GetHeader("X-User-ID"); uid != "" {
		return uid
	}

	// 2. Lấy tham số truy vấn
	if uid := c.Query("uid"); uid != "" {
		return uid
	}

	// 3. Nhận từ các trường biểu mẫu
	if uid := c.PostForm("uid"); uid != "" {
		return uid
	}

	// 4. Nhận từ phần mềm trung gian xác thực (nếu có)
	if uid, exists := c.Get("user_id"); exists {
		if uidStr, ok := uid.(string); ok && uidStr != "" {
			return uidStr
		}
	}

	return ""
}

// getAgentIDFromRequest Trích xuất ID tác nhân từ yêu cầu
// Ưu tiên: tiêu đề yêu cầu X-Agent-ID > tham số truy vấn Agent_id > trường biểu mẫu Agent_id
func getAgentIDFromRequest(c *gin.Context) string {
	// 1. Lấy từ tiêu đề yêu cầu
	if agentID := c.GetHeader("X-Agent-ID"); agentID != "" {
		return agentID
	}

	// 2. Lấy tham số truy vấn
	if agentID := c.Query("agent_id"); agentID != "" {
		return agentID
	}

	// 3. Nhận từ các trường biểu mẫu
	if agentID := c.PostForm("agent_id"); agentID != "" {
		return agentID
	}

	// 4. Nhận từ phần mềm trung gian xác thực (nếu có)
	if agentID, exists := c.Get("agent_id"); exists {
		if agentIDStr, ok := agentID.(string); ok && agentIDStr != "" {
			return agentIDStr
		}
	}

	return ""
}

// RegisterRoutes đăng ký tuyến đường
func (h *Handler) RegisterRoutes(router *gin.Engine) {
	speakerGroup := router.Group("/api/v1/speaker")
	{
		// Đăng ký giọng nói
		speakerGroup.POST("/register", h.RegisterSpeaker)

		// Nhận dạng giọng nói
		speakerGroup.POST("/identify", h.IdentifySpeaker)

		// Xác minh giọng nói
		speakerGroup.POST("/verify/:speaker_id", h.VerifySpeaker)

		// Nhận tất cả các loa
		speakerGroup.GET("/list", h.GetAllSpeakers)

		// Xóa loa (hỗ trợ hai phương pháp)
		speakerGroup.DELETE("", h.DeleteSpeaker)             // DELETE /api/v1/speaker?uuid=xxx
		speakerGroup.DELETE("/:speaker_id", h.DeleteSpeaker) // DELETE /api/v1/speaker/:speaker_id

		// Nhận số liệu thống kê cơ sở dữ liệu
		speakerGroup.GET("/stats", h.GetStats)

		//Giao diện đăng ký và nhận dạng Base64
		speakerGroup.POST("/register_base64", h.RegisterSpeakerBase64)
		speakerGroup.POST("/identify_base64", h.IdentifySpeakerBase64)

		// Giao diện nhận dạng phát trực tuyến WebSocket
		speakerGroup.GET("/identify_ws", h.IdentifySpeakerWebSocket)
	}
}

// RegisterSpeaker đăng ký giọng nói
func (h *Handler) RegisterSpeaker(c *gin.Context) {
	// Nhận UID
	uid := getUIDFromRequest(c)
	if uid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "uid is required (X-User-ID header, uid query param, or uid form field)",
		})
		return
	}

	// Nhận ID đại lý (bắt buộc)
	agentID := getAgentIDFromRequest(c)
	if agentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "agent_id is required (X-Agent-ID header, agent_id query param, or agent_id form field)",
		})
		return
	}

	// Nhận dữ liệu biểu mẫu
	speakerID := c.PostForm("speaker_id")
	speakerName := c.PostForm("speaker_name")
	uuid := c.PostForm("uuid") // Mới: tham số UUID

	if speakerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "speaker_id is required",
		})
		return
	}

	if speakerName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "speaker_name is required",
		})
		return
	}

	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "uuid is required",
		})
		return
	}

	// Nhận tập tin âm thanh
	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "audio file is required",
		})
		return
	}
	defer file.Close()

	// Phân tích dữ liệu âm thanh
	audioData, sampleRate, err := h.parseAudioFile(file, header)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("failed to parse audio file: %v", err),
		})
		return
	}

	// Sử dụng VAD để lọc khoảng lặng và giữ lại khoảng im lặng 100ms trước và sau
	filteredAudio, err := h.manager.FilterSilenceWithVADKeepEdges(audioData, sampleRate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("failed to filter silence: %v", err),
		})
		return
	}

	// Đăng ký giọng nói (sử dụng âm thanh được lọc)
	err = h.manager.RegisterSpeaker(uid, agentID, speakerID, speakerName, uuid, filteredAudio, sampleRate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("failed to register speaker: %v", err),
		})
		return
	}

	// Lưu tệp âm thanh (lưu không đồng bộ, không chặn phản hồi)
	go func() {
		if err := saveRegisterAudioToWAV(filteredAudio, sampleRate, uid, agentID); err != nil {
			logger.Warnf("Failed to save register audio file: %v", err)
		} else {
			logger.Infof("Register audio file saved successfully, samples: %d", len(filteredAudio))
		}
	}()

	c.JSON(http.StatusOK, gin.H{
		"message":      "Speaker registered successfully",
		"uid":          uid,
		"agent_id":     agentID,
		"speaker_id":   speakerID,
		"speaker_name": speakerName,
		"uuid":         uuid,
	})
}

// IdentitySpeaker xác định giọng nói
func (h *Handler) IdentifySpeaker(c *gin.Context) {
	// Nhận UID (tùy chọn)
	uid := getUIDFromRequest(c)

	// Nhận ID đại lý (tùy chọn)
	agentID := getAgentIDFromRequest(c)

	// Nhận tham số loa_id (tùy chọn)
	speakerID := c.Query("speaker_id")
	if speakerID == "" {
		speakerID = c.PostForm("speaker_id")
	}

	// Nhận tham số loa_name (tùy chọn)
	speakerName := c.Query("speaker_name")
	if speakerName == "" {
		speakerName = c.PostForm("speaker_name")
	}

	// Nhận tập tin âm thanh
	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "audio file is required",
		})
		return
	}
	defer file.Close()

	// Phân tích dữ liệu âm thanh
	audioData, sampleRate, err := h.parseAudioFile(file, header)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("failed to parse audio file: %v", err),
		})
		return
	}

	// Nhận thông số ngưỡng (tùy chọn)
	var threshold float32
	if thresholdStr := c.Query("threshold"); thresholdStr != "" {
		if parsed, err := parseFloat32(thresholdStr); err == nil && parsed > 0 {
			threshold = parsed
		}
	} else if thresholdStr := c.PostForm("threshold"); thresholdStr != "" {
		if parsed, err := parseFloat32(thresholdStr); err == nil && parsed > 0 {
			threshold = parsed
		}
	}

	// Nhận dạng giọng nói (sử dụng ngưỡng nếu được cung cấp, nếu không thì sử dụng mặc định)
	var result *IdentifyResult
	if threshold > 0 {
		result, err = h.manager.IdentifySpeaker(uid, agentID, speakerID, speakerName, audioData, sampleRate, threshold)
	} else {
		result, err = h.manager.IdentifySpeaker(uid, agentID, speakerID, speakerName, audioData, sampleRate)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("failed to identify speaker: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

// VerifySpeaker Xác minh giọng nói
func (h *Handler) VerifySpeaker(c *gin.Context) {
	// Nhận UID
	uid := getUIDFromRequest(c)
	if uid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "uid is required (X-User-ID header, uid query param, or uid form field)",
		})
		return
	}

	// Nhận ID đại lý (tùy chọn)
	agentID := getAgentIDFromRequest(c)

	speakerID := c.Param("speaker_id")
	if speakerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "speaker_id is required",
		})
		return
	}

	// Nhận tập tin âm thanh
	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "audio file is required",
		})
		return
	}
	defer file.Close()

	// Phân tích dữ liệu âm thanh
	audioData, sampleRate, err := h.parseAudioFile(file, header)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("failed to parse audio file: %v", err),
		})
		return
	}

	// Xác minh giọng nói
	result, err := h.manager.VerifySpeaker(uid, agentID, speakerID, audioData, sampleRate)
	if err != nil {
		if strings.Contains(err.Error(), "belongs to different uid") {
			c.JSON(http.StatusForbidden, gin.H{
				"error": err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("failed to verify speaker: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetAllSpeakers Nhận tất cả các loa
func (h *Handler) GetAllSpeakers(c *gin.Context) {
	// Nhận UID
	uid := getUIDFromRequest(c)
	if uid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "uid is required (X-User-ID header, uid query param, or uid form field)",
		})
		return
	}

	// Nhận ID đại lý (tùy chọn)
	agentID := getAgentIDFromRequest(c)

	speakers := h.manager.GetAllSpeakers(uid, agentID)
	c.JSON(http.StatusOK, gin.H{
		"uid":      uid,
		"agent_id": agentID,
		"speakers": speakers,
		"total":    len(speakers),
	})
}

// DeleteSpeakerXóa loa
// Hai phương thức được hỗ trợ (yêu cầu uid, tùy chọn Agent_id):
// 1. Xóa một dấu giọng nói bằng uuid: DELETE /api/v1/loa?uuid=xxx (đường dẫn không có loa_id)
// 2. Xóa toàn bộ bộ voiceprint của loa_id: DELETE /api/v1/loa/:loa_id
func (h *Handler) DeleteSpeaker(c *gin.Context) {
	// Nhận UID
	uid := getUIDFromRequest(c)
	if uid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "uid is required (X-User-ID header, uid query param, or uid form field)",
		})
		return
	}

	// Nhận ID đại lý (tùy chọn)
	agentID := getAgentIDFromRequest(c)

	// Thích sử dụng các tham số truy vấn uuid
	uuid := c.Query("uuid")
	if uuid != "" {
		// Xóa bằng UUID
		err := h.manager.DeleteSpeakerByUUID(uid, agentID, uuid)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				c.JSON(http.StatusNotFound, gin.H{
					"error": err.Error(),
				})
				return
			}
			if strings.Contains(err.Error(), "belongs to different uid") {
				c.JSON(http.StatusForbidden, gin.H{
					"error": err.Error(),
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("failed to delete speaker: %v", err),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":  "Speaker deleted successfully",
			"uid":      uid,
			"agent_id": agentID,
			"uuid":     uuid,
		})
		return
	}

	// Xóa thông số đường dẫn loa_id (loa_id là tên nhóm)
	speakerID := c.Param("speaker_id")
	if speakerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "uuid or speaker_id is required",
		})
		return
	}

	err := h.manager.DeleteSpeaker(uid, agentID, speakerID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{
				"error": err.Error(),
			})
			return
		}
		if strings.Contains(err.Error(), "belongs to different uid") {
			c.JSON(http.StatusForbidden, gin.H{
				"error": err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("failed to delete speaker: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "Speaker deleted successfully",
		"uid":        uid,
		"agent_id":   agentID,
		"speaker_id": speakerID,
	})
}

// GetStats Lấy số liệu thống kê cơ sở dữ liệu
func (h *Handler) GetStats(c *gin.Context) {
	// UID là tùy chọn, nếu không cung cấp số liệu thống kê toàn cầu sẽ được trả về
	uid := getUIDFromRequest(c)
	// ID đại lý là tùy chọn
	agentID := getAgentIDFromRequest(c)
	stats := h.manager.GetStats(uid, agentID)
	c.JSON(http.StatusOK, stats)
}

// ParseAudioFile phân tích các tập tin âm thanh
func (h *Handler) parseAudioFile(file multipart.File, header *multipart.FileHeader) ([]float32, int, error) {
	// Kiểm tra loại tập tin
	filename := strings.ToLower(header.Filename)
	if !strings.HasSuffix(filename, ".wav") {
		return nil, 0, fmt.Errorf("only WAV files are supported")
	}

	// Đọc tập tin WAV
	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return nil, 0, fmt.Errorf("invalid WAV file")
	}

	// Nhận thông tin định dạng âm thanh
	sampleRate := int(decoder.SampleRate)
	numChannels := int(decoder.NumChans)

	// Chỉ hỗ trợ mono hoặc stereo
	if numChannels > 2 {
		return nil, 0, fmt.Errorf("unsupported number of channels: %d", numChannels)
	}

	// Đọc dữ liệu âm thanh
	buffer, err := decoder.FullPCMBuffer()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to decode audio: %v", err)
	}

	// Chuyển đổi sang định dạng float32
	samples := make([]float32, len(buffer.Data))
	for i, sample := range buffer.Data {
		// Chuyển đổi int thành float32, phạm vi [-1.0, 1.0]
		samples[i] = float32(sample) / config.GlobalConfig.Audio.NormalizeFactor
	}

	// Nếu là âm thanh nổi, hãy chuyển sang đơn âm (trung bình)
	if numChannels == 2 {
		monoSamples := make([]float32, len(samples)/2)
		for i := 0; i < len(monoSamples); i++ {
			monoSamples[i] = (samples[i*2] + samples[i*2+1]) / 2.0
		}
		samples = monoSamples
	}

	return samples, sampleRate, nil
}

// Thêm giao diện API dựa trên Base64 (tùy chọn)

// RegisterSpeakerBase64 Đăng ký giọng nói bằng dữ liệu âm thanh được mã hóa Base64
func (h *Handler) RegisterSpeakerBase64(c *gin.Context) {
	var req struct {
		SpeakerID   string `json:"speaker_id" binding:"required"`
		SpeakerName string `json:"speaker_name" binding:"required"`
		AudioData   string `json:"audio_data" binding:"required"` // Dữ liệu WAV được mã hóa Base64
		SampleRate  int    `json:"sample_rate" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	// Logic xử lý âm thanh và giải mã Base64 có thể được thêm vào đây
	// Để đơn giản hóa ví dụ, việc triển khai cụ thể tạm thời bị bỏ qua

	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "Base64 API not implemented yet",
	})
}

// Xác địnhSpeakerBase64 sử dụng dữ liệu âm thanh được mã hóa Base64 để xác định dấu giọng nói
func (h *Handler) IdentifySpeakerBase64(c *gin.Context) {
	var req struct {
		AudioData  string `json:"audio_data" binding:"required"` // Dữ liệu WAV được mã hóa Base64
		SampleRate int    `json:"sample_rate" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	// Logic xử lý âm thanh và giải mã Base64 có thể được thêm vào đây
	// Để đơn giản hóa ví dụ, việc triển khai cụ thể tạm thời bị bỏ qua

	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "Base64 API not implemented yet",
	})
}

// Trình nâng cấp WebSocket Trình nâng cấp WebSocket
var WebSocketUpgrader = websocket.Upgrader{
	CheckOrigin:       func(r *http.Request) bool { return true },
	ReadBufferSize:    config.GlobalConfig.Server.WebSocket.ReadBufferSize,
	WriteBufferSize:   config.GlobalConfig.Server.WebSocket.WriteBufferSize,
	EnableCompression: config.GlobalConfig.Server.WebSocket.EnableCompression,
}

// Xác địnhSpeakerWebSocket WebSocket nhận dạng giọng nói trực tuyến
// Hỗ trợ tái sử dụng kết nối và nhận dạng nhiều vòng:
// - Gửi {"action": "peek"} để nhận kết quả nhận dạng trung gian của vòng hiện tại (không kết thúc vòng hiện tại)
// - Gửi {"action": "finish"} để hoàn thành vòng nhận dạng hiện tại và tự động reset trạng thái sau khi trả kết quả để chuẩn bị cho vòng tiếp theo
// - Gửi {"action": "cancel"} để hủy vòng công nhận hiện tại, đặt lại trạng thái và chuẩn bị cho vòng tiếp theo
// - Gửi {"action": "close"} để đóng kết nối
// - Hỗ trợ duy trì nhịp tim ping/pong của lớp giao thức WebSocket (tự động trả lời pong và làm mới bộ đếm thời gian chờ)
func (h *Handler) IdentifySpeakerWebSocket(c *gin.Context) {
	// Nâng cấp lên kết nối WebSocket
	conn, err := WebSocketUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Errorf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	logger.Infof("WebSocket connection established for speaker identification (multi-round enabled)")

	// Lấy tham số tốc độ lấy mẫu (mặc định 16000)
	sampleRate := 16000
	if sr := c.Query("sample_rate"); sr != "" {
		if srInt, err := parseInt(sr); err == nil {
			sampleRate = srInt
			logger.Debugf("WebSocket: Using custom sample rate: %d Hz", sampleRate)
		} else {
			logger.Warnf("WebSocket: Invalid sample_rate parameter '%s', using default 16000", sr)
		}
	} else {
		logger.Debugf("WebSocket: No sample_rate parameter, using default: %d Hz", sampleRate)
	}

	// Nhận thông số ngưỡng (tùy chọn)
	var threshold float32
	if thresholdStr := c.Query("threshold"); thresholdStr != "" {
		if parsed, err := parseFloat32(thresholdStr); err == nil && parsed > 0 {
			threshold = parsed
			logger.Debugf("WebSocket: Using custom threshold: %.4f", threshold)
		} else {
			logger.Warnf("WebSocket: Invalid threshold parameter '%s', using default", thresholdStr)
		}
	}

	// Nhận UID (từ tham số truy vấn hoặc tiêu đề yêu cầu, tùy chọn)
	uid := getUIDFromRequest(c)

	// Nhận ID đại lý (tùy chọn)
	agentID := getAgentIDFromRequest(c)

	// Nhận tham số loa_id (tùy chọn)
	speakerID := c.Query("speaker_id")

	// Nhận tham số loa_name (tùy chọn)
	speakerName := c.Query("speaker_name")

	// Các chức năng trợ giúp để tạo trình nhận dạng phát trực tuyến
	createIdentifier := func() *StreamingIdentifier {
		logger.Debugf("WebSocket: Creating streaming identifier for uid: %s, agent_id: %s, speaker_id: %s, speaker_name: %s, sample rate: %d Hz, threshold: %.4f", uid, agentID, speakerID, speakerName, sampleRate, threshold)
		if threshold > 0 {
			return h.manager.NewStreamingIdentifier(uid, agentID, speakerID, speakerName, sampleRate, threshold)
		}
		return h.manager.NewStreamingIdentifier(uid, agentID, speakerID, speakerName, sampleRate)
	}

	// Tạo trình nhận dạng phát trực tuyến ban đầu
	identifier := createIdentifier()
	defer func() {
		if identifier != nil {
			identifier.Close()
		}
	}()

	// Đặt thời gian chờ đọc
	wsConfig := config.GlobalConfig.Server.WebSocket
	if wsConfig.ReadTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(time.Duration(wsConfig.ReadTimeout) * time.Second))
		logger.Debugf("WebSocket: Set read timeout: %d seconds", wsConfig.ReadTimeout)
	}

	// Đặt trình xử lý ping lớp giao thức WebSocket để làm mới thời gian chờ và tự động trả lời pong khi nhận được ping
	conn.SetPingHandler(func(appData string) error {
		logger.Debugf("WebSocket: Received protocol ping, refreshing timeout and sending pong")
		if wsConfig.ReadTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(time.Duration(wsConfig.ReadTimeout) * time.Second))
		}
		// Trả lời pong (sử dụng cùng một appData)
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
	})

	// Gửi tin nhắn xác nhận kết nối
	connectionMsg := map[string]interface{}{
		"type":        "connection",
		"message":     "WebSocket connected, ready for audio (multi-round enabled)",
		"sample_rate": sampleRate,
	}
	if err := conn.WriteJSON(connectionMsg); err != nil {
		logger.Errorf("Failed to send connection message: %v", err)
		return
	}
	logger.Debugf("WebSocket: Sent connection confirmation message: %+v", connectionMsg)

	// Bộ đệm âm thanh (được sử dụng để lưu tệp âm thanh)
	var audioBuffer []float32
	saveAudioEnabled := config.GlobalConfig.Speaker.SaveAudioOnFinish
	useThreshold := h.manager.threshold
	if threshold > 0 {
		useThreshold = threshold
	}
	// Chống rung nhìn trộm: Theo mặc định, quá trình xử lý được thực hiện tối đa 150 mili giây một lần để tránh việc nhìn lén tần số cao làm dịch vụ bị choáng ngợp.
	const minPeekInterval = 150 * time.Millisecond
	var lastPeekAt time.Time

	// đọc tin nhắn
	totalAudioSamples := 0
	audioChunkCount := 0
	roundCount := 0 // Xác định số vòng
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Warnf("WebSocket read error: %v", err)
			} else {
				logger.Debugf("WebSocket: Connection closed normally or read error: %v", err)
			}
			break
		}

		logger.Debugf("WebSocket: Received message, type=%d, size=%d bytes", messageType, len(message))

		// làm mới thời gian chờ đọc
		if wsConfig.ReadTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(time.Duration(wsConfig.ReadTimeout) * time.Second))
		}

		// Kiểm tra kích thước tin nhắn
		if wsConfig.MaxMessageSize > 0 && len(message) > wsConfig.MaxMessageSize {
			logger.Warnf("WebSocket: Message too large: %d bytes (max: %d)", len(message), wsConfig.MaxMessageSize)
			conn.WriteJSON(map[string]interface{}{
				"type":    "error",
				"message": "message too large",
			})
			continue
		}

		// Xử lý tin nhắn văn bản (điều khiển tin nhắn)
		if messageType == websocket.TextMessage {
			logger.Debugf("WebSocket: Received text message: %s", string(message))
			var controlMsg map[string]interface{}
			if err := json.Unmarshal(message, &controlMsg); err != nil {
				logger.Warnf("WebSocket: Failed to unmarshal text message: %v", err)
				continue
			}

			logger.Debugf("WebSocket: Parsed control message: %+v", controlMsg)

			if action, ok := controlMsg["action"].(string); ok {
				logger.Debugf("WebSocket: Control action: %s", action)
				switch action {
				case "peek":
					// Truy vấn kết quả trung gian: không kết thúc vòng hiện tại và không đặt lại trạng thái phát trực tuyến
					requestID, _ := controlMsg["request_id"].(string)
					now := time.Now()
					if !lastPeekAt.IsZero() && now.Sub(lastPeekAt) < minPeekInterval {
						resp := map[string]interface{}{
							"type":        "partial_result",
							"is_final":    false,
							"round":       roundCount + 1,
							"audio_ms":    float64(len(audioBuffer)) / float64(sampleRate) * 1000,
							"audio_count": len(audioBuffer),
							"throttled":   true,
							"message":     "peek throttled",
						}
						if requestID != "" {
							resp["request_id"] = requestID
						}
						if err := conn.WriteJSON(resp); err != nil {
							logger.Warnf("WebSocket: Failed to send throttled partial_result: %v", err)
						}
						continue
					}
					lastPeekAt = now

					resp := map[string]interface{}{
						"type":        "partial_result",
						"is_final":    false,
						"round":       roundCount + 1,
						"audio_ms":    float64(len(audioBuffer)) / float64(sampleRate) * 1000,
						"audio_count": len(audioBuffer),
					}
					if requestID != "" {
						resp["request_id"] = requestID
					}

					// Khi không có âm thanh, hãy trả về trực tiếp kết quả trống để tránh tạo trình nhận dạng.
					if len(audioBuffer) == 0 {
						resp["result"] = &IdentifyResult{
							Identified:  false,
							SpeakerID:   "",
							SpeakerName: "",
							Confidence:  0.0,
							Threshold:   useThreshold,
						}
						resp["message"] = "no audio received yet"
						if err := conn.WriteJSON(resp); err != nil {
							logger.Warnf("WebSocket: Failed to send empty partial_result: %v", err)
						}
						continue
					}

					// Sao chép ảnh chụp nhanh âm thanh hiện tại để tránh bị ghi đè khi sử dụng lại ở các vòng tiếp theo
					audioSnapshot := make([]float32, len(audioBuffer))
					copy(audioSnapshot, audioBuffer)

					var peekResult *IdentifyResult
					var identifyErr error
					if threshold > 0 {
						peekResult, identifyErr = h.manager.IdentifySpeaker(uid, agentID, speakerID, speakerName, audioSnapshot, sampleRate, threshold)
					} else {
						peekResult, identifyErr = h.manager.IdentifySpeaker(uid, agentID, speakerID, speakerName, audioSnapshot, sampleRate)
					}

					if identifyErr != nil {
						resp["result"] = &IdentifyResult{
							Identified:  false,
							SpeakerID:   "",
							SpeakerName: "",
							Confidence:  0.0,
							Threshold:   useThreshold,
						}
						resp["error"] = identifyErr.Error()
						if err := conn.WriteJSON(resp); err != nil {
							logger.Warnf("WebSocket: Failed to send partial_result error: %v", err)
						}
						continue
					}

					resp["result"] = peekResult
					if err := conn.WriteJSON(resp); err != nil {
						logger.Warnf("WebSocket: Failed to send partial_result: %v", err)
					}

				case "finish":
					// Hoàn thành vòng nhận dạng hiện tại
					roundCount++
					logger.Debugf("WebSocket: Finish action received (round %d), total audio samples: %d, chunks: %d", roundCount, totalAudioSamples, audioChunkCount)
					logger.Debugf("WebSocket: Calling FinishAndIdentify()...")
					result, err := identifier.FinishAndIdentify()
					if err != nil {
						logger.Errorf("WebSocket: FinishAndIdentify failed: %v", err)
						conn.WriteJSON(map[string]interface{}{
							"type":    "error",
							"message": err.Error(),
							"round":   roundCount,
						})
						// Đặt lại trạng thái để chuẩn bị cho vòng tiếp theo (cho phép tiếp tục ngay cả khi xảy ra lỗi)
						identifier.Close()
						identifier = createIdentifier()
						audioBuffer = nil
						totalAudioSamples = 0
						audioChunkCount = 0
						continue
					}

					// Nếu bật tính năng lưu âm thanh, hãy lưu tệp âm thanh
					if saveAudioEnabled && len(audioBuffer) > 0 {
						// Sao chép dữ liệu âm thanh để tránh dữ liệu bị sửa đổi trong quá trình thực thi không đồng bộ
						audioDataCopy := make([]float32, len(audioBuffer))
						copy(audioDataCopy, audioBuffer)
						currentRound := roundCount
						go func() {
							// Lưu không đồng bộ, không có phản hồi chặn
							if err := saveAudioToWAV(audioDataCopy, sampleRate, uid, agentID); err != nil {
								logger.Warnf("WebSocket: Failed to save audio file (round %d): %v", currentRound, err)
							} else {
								logger.Infof("WebSocket: Audio file saved successfully (round %d), samples: %d", currentRound, len(audioDataCopy))
							}
						}()
					}

					// Gửi kết quả nhận dạng
					conn.WriteJSON(map[string]interface{}{
						"type":   "result",
						"result": result,
						"round":  roundCount,
					})
					logger.Infof("WebSocket: Sent identification result to client (round %d)", roundCount)

					// Đặt lại trạng thái và chuẩn bị cho vòng công nhận tiếp theo
					identifier.Close()
					identifier = createIdentifier()
					audioBuffer = nil
					totalAudioSamples = 0
					audioChunkCount = 0

					// Gửi tin nhắn sẵn sàng để thông báo cho khách hàng rằng vòng tiếp theo có thể bắt đầu
					conn.WriteJSON(map[string]interface{}{
						"type":    "ready",
						"message": "Ready for next round",
						"round":   roundCount + 1,
					})
					logger.Debugf("WebSocket: Reset state, ready for round %d", roundCount+1)

				case "cancel":
					// Hủy trạng thái nhận dạng vòng hiện tại và đặt lại
					logger.Infof("WebSocket: Cancel action received (round %d), resetting state", roundCount+1)
					identifier.Close()
					identifier = createIdentifier()
					audioBuffer = nil
					totalAudioSamples = 0
					audioChunkCount = 0

					conn.WriteJSON(map[string]interface{}{
						"type":    "cancelled",
						"message": "Current round cancelled, ready for next round",
						"round":   roundCount + 1,
					})

				case "close":
					// Rõ ràng đóng kết nối
					logger.Infof("WebSocket: Close action received, closing connection after %d rounds", roundCount)
					conn.WriteJSON(map[string]interface{}{
						"type":         "closing",
						"message":      "Connection closing",
						"total_rounds": roundCount,
					})
					return

				default:
					logger.Warnf("WebSocket: Unknown action: %s", action)
				}
			} else {
				logger.Debugf("WebSocket: Text message without action field: %+v", controlMsg)
			}
			continue
		}

		// Xử lý tin nhắn nhị phân (dữ liệu âm thanh)
		if messageType == websocket.BinaryMessage {
			logger.Debugf("WebSocket: Received binary message: %d bytes", len(message))

			// Chuyển đổi dữ liệu byte thành mảng float32
			// Giả sử rằng đầu vào là dữ liệu nhị phân float32 (endian nhỏ)
			if len(message)%4 != 0 {
				logger.Warnf("WebSocket: Invalid audio data length: %d bytes (not divisible by 4)", len(message))
				conn.WriteJSON(map[string]interface{}{
					"type":    "error",
					"message": "invalid audio data length",
				})
				continue
			}

			sampleCount := len(message) / 4
			if sampleCount == 0 {
				conn.WriteJSON(map[string]interface{}{
					"type":    "error",
					"message": "empty audio chunk",
				})
				continue
			}
			audioData := make([]float32, sampleCount)
			for i := 0; i < len(audioData); i++ {
				bits := binary.LittleEndian.Uint32(message[i*4 : (i+1)*4])
				audioData[i] = math.Float32frombits(bits)
			}

			// Kiểm tra phạm vi dữ liệu âm thanh
			var minVal, maxVal float32 = audioData[0], audioData[0]
			for _, v := range audioData {
				if v < minVal {
					minVal = v
				}
				if v > maxVal {
					maxVal = v
				}
			}
			logger.Debugf("WebSocket: Audio chunk #%d: samples=%d, duration=%.2fms, range=[%.4f, %.4f]",
				audioChunkCount+1, sampleCount, float64(sampleCount)/float64(sampleRate)*1000, minVal, maxVal)

			// Nhận khối dữ liệu âm thanh
			if err := identifier.AcceptAudio(audioData); err != nil {
				logger.Errorf("WebSocket: Failed to accept audio chunk #%d: %v", audioChunkCount+1, err)
				conn.WriteJSON(map[string]interface{}{
					"type":    "error",
					"message": err.Error(),
				})
				return
			}

			// Luôn giữ lại âm thanh tròn hiện tại để nhận dạng âm thanh giữa và vị trí tùy chọn
			audioBuffer = append(audioBuffer, audioData...)

			totalAudioSamples += sampleCount
			audioChunkCount++

			// In số liệu thống kê cứ sau 10 khối
			if audioChunkCount%10 == 0 {
				logger.Debugf("WebSocket: Audio progress - chunks: %d, total samples: %d, total duration: %.2fs",
					audioChunkCount, totalAudioSamples, float64(totalAudioSamples)/float64(sampleRate))
			}

			// Gửi tin nhắn xác nhận (tùy chọn)
			ackMsg := map[string]interface{}{
				"type":        "audio_received",
				"samples":     len(audioData),
				"duration_ms": float64(len(audioData)) / float64(sampleRate) * 1000,
			}
			if err := conn.WriteJSON(ackMsg); err != nil {
				logger.Warnf("WebSocket: Failed to send audio_received ack: %v", err)
			}
		} else {
			logger.Debugf("WebSocket: Received unknown message type: %d", messageType)
		}
	}

	logger.Infof("WebSocket: Connection closed, total rounds: %d, current round audio chunks: %d, samples: %d, duration: %.2fs",
		roundCount, audioChunkCount, totalAudioSamples, float64(totalAudioSamples)/float64(sampleRate))
}

// ParseInt phân tích số nguyên (hàm trợ giúp)
func parseInt(s string) (int, error) {
	var result int
	_, err := fmt.Sscanf(s, "%d", &result)
	return result, err
}

// ParsFloat32 phân tích số dấu phẩy động (hàm phụ trợ)
func parseFloat32(s string) (float32, error) {
	var result float32
	_, err := fmt.Sscanf(s, "%f", &result)
	return result, err
}

// saveAudioToWAV lưu dữ liệu âm thanh dưới dạng tệp WAV
func saveAudioToWAV(audioData []float32, sampleRate int, uid, agentID string) error {
	if len(audioData) == 0 {
		return fmt.Errorf("audio data is empty")
	}

	// Xác định thư mục lưu
	saveDir := config.GlobalConfig.Speaker.AudioSaveDir
	if saveDir == "" {
		// Nếu không được chỉ định, data_dir sẽ được sử dụng
		saveDir = config.GlobalConfig.Speaker.DataDir
	}
	if saveDir == "" {
		saveDir = "data/speaker"
	}

	// Tạo thư mục lưu nếu nó không tồn tại
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return fmt.Errorf("failed to create save directory: %w", err)
	}

	// Tạo tên tệp: timestamp_uid_agentid.wav
	timestamp := time.Now().Format("20060102_150405")
	var filename string
	if uid != "" && agentID != "" {
		filename = fmt.Sprintf("%s_%s_%s.wav", timestamp, uid, agentID)
	} else if uid != "" {
		filename = fmt.Sprintf("%s_%s.wav", timestamp, uid)
	} else if agentID != "" {
		filename = fmt.Sprintf("%s_%s.wav", timestamp, agentID)
	} else {
		filename = fmt.Sprintf("%s.wav", timestamp)
	}

	// Làm sạch các ký tự không hợp lệ trong tên tệp
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.ReplaceAll(filename, ":", "_")

	filePath := filepath.Join(saveDir, filename)

	// Tạo tập tin
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Chuyển đổi float32 sang int16
	// Phạm vi của float32 là [-1.0, 1.0] và cần được chuyển đổi thành phạm vi int16 [-32768, 32767]
	int16Data := make([]int, len(audioData))
	normalizeFactor := config.GlobalConfig.Audio.NormalizeFactor
	for i, sample := range audioData {
		// Phạm vi giới hạn ở [-1.0, 1.0]
		if sample > 1.0 {
			sample = 1.0
		} else if sample < -1.0 {
			sample = -1.0
		}
		// Chuyển đổi sang int16
		int16Data[i] = int(sample * normalizeFactor)
	}

	// Tạo định dạng âm thanh
	format := &audio.Format{
		NumChannels: 1, // bệnh tăng bạch cầu đơn nhân
		SampleRate:  sampleRate,
	}

	// Tạo bộ mã hóa WAV
	encoder := wav.NewEncoder(file, format.SampleRate, 16, format.NumChannels, 1)

	// Tạo bộ đệm âm thanh
	buf := &audio.IntBuffer{
		Format:         format,
		SourceBitDepth: 16,
		Data:           int16Data,
	}

	// Ghi dữ liệu âm thanh
	if err := encoder.Write(buf); err != nil {
		return fmt.Errorf("failed to write audio data: %w", err)
	}

	// Tắt bộ mã hóa
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("failed to close encoder: %w", err)
	}

	logger.Debugf("Saved audio file: %s, samples: %d, duration: %.2fs", filePath, len(audioData), float64(len(audioData))/float64(sampleRate))
	return nil
}

// saveRegisterAudioToWAV lưu dữ liệu âm thanh đã đăng ký dưới dạng tệp WAV (tên tệp có tiền tố là "register_")
func saveRegisterAudioToWAV(audioData []float32, sampleRate int, uid, agentID string) error {
	if len(audioData) == 0 {
		return fmt.Errorf("audio data is empty")
	}

	// Xác định thư mục lưu
	saveDir := config.GlobalConfig.Speaker.AudioSaveDir
	if saveDir == "" {
		// Nếu không được chỉ định, data_dir sẽ được sử dụng
		saveDir = config.GlobalConfig.Speaker.DataDir
	}
	if saveDir == "" {
		saveDir = "data/speaker"
	}

	// Tạo thư mục lưu nếu nó không tồn tại
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return fmt.Errorf("failed to create save directory: %w", err)
	}

	// Tên tệp đã tạo: register_timestamp_uid_agentid.wav
	timestamp := time.Now().Format("20060102_150405")
	var filename string
	if uid != "" && agentID != "" {
		filename = fmt.Sprintf("register_%s_%s_%s.wav", timestamp, uid, agentID)
	} else if uid != "" {
		filename = fmt.Sprintf("register_%s_%s.wav", timestamp, uid)
	} else if agentID != "" {
		filename = fmt.Sprintf("register_%s_%s.wav", timestamp, agentID)
	} else {
		filename = fmt.Sprintf("register_%s.wav", timestamp)
	}

	// Làm sạch các ký tự không hợp lệ trong tên tệp
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.ReplaceAll(filename, ":", "_")

	filePath := filepath.Join(saveDir, filename)

	// Tạo tập tin
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Chuyển đổi float32 sang int16
	// Phạm vi của float32 là [-1.0, 1.0] và cần được chuyển đổi thành phạm vi int16 [-32768, 32767]
	int16Data := make([]int, len(audioData))
	normalizeFactor := config.GlobalConfig.Audio.NormalizeFactor
	for i, sample := range audioData {
		// Phạm vi giới hạn ở [-1.0, 1.0]
		if sample > 1.0 {
			sample = 1.0
		} else if sample < -1.0 {
			sample = -1.0
		}
		// Chuyển đổi sang int16
		int16Data[i] = int(sample * normalizeFactor)
	}

	// Tạo định dạng âm thanh
	format := &audio.Format{
		NumChannels: 1, // bệnh tăng bạch cầu đơn nhân
		SampleRate:  sampleRate,
	}

	// Tạo bộ mã hóa WAV
	encoder := wav.NewEncoder(file, format.SampleRate, 16, format.NumChannels, 1)

	// Tạo bộ đệm âm thanh
	buf := &audio.IntBuffer{
		Format:         format,
		SourceBitDepth: 16,
		Data:           int16Data,
	}

	// Ghi dữ liệu âm thanh
	if err := encoder.Write(buf); err != nil {
		return fmt.Errorf("failed to write audio data: %w", err)
	}

	// Tắt bộ mã hóa
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("failed to close encoder: %w", err)
	}

	logger.Debugf("Saved register audio file: %s, samples: %d, duration: %.2fs", filePath, len(audioData), float64(len(audioData))/float64(sampleRate))
	return nil
}
