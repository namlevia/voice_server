package ws

import (
	"voice_server/config"
	"voice_server/internal/logger"
	"voice_server/internal/session"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"

	"github.com/gorilla/websocket"
)

// Trình nâng cấp được sử dụng để nâng cấp các kết nối WebSocket
var Upgrader = websocket.Upgrader{
	CheckOrigin:       func(r *http.Request) bool { return true },
	ReadBufferSize:    config.GlobalConfig.Server.WebSocket.ReadBufferSize,
	WriteBufferSize:   config.GlobalConfig.Server.WebSocket.WriteBufferSize,
	EnableCompression: config.GlobalConfig.Server.WebSocket.EnableCompression,
}

// TạoSessionID Tạo ID phiên
func GenerateSessionID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// HandleWebSocket xử lý các kết nối WebSocket
// Trình quản lý phiên tiêm phụ thuộc, GlobalRecognizer
func HandleWebSocket(w http.ResponseWriter, r *http.Request, sessionManager *session.Manager, globalRecognizer *sherpa.OfflineRecognizer) {
	conn, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Errorf("WebSocket upgrade failed: %v", err)
		return
	}

	wsConfig := config.GlobalConfig.Server.WebSocket

	if wsConfig.ReadTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(time.Duration(wsConfig.ReadTimeout) * time.Second))
	}

	// Kiểm tra xem tính năng nhận dạng có được bật hay không và quay lại trực tiếp nếu tính năng này không được bật
	if !config.GlobalConfig.Recognition.Enabled {
		logger.Warnf("Recognition is disabled, closing WebSocket connection")
		conn.WriteJSON(map[string]interface{}{
			"type":    "error",
			"message": "Recognition service is disabled",
		})
		conn.Close()
		return
	}

	sessionID := GenerateSessionID()

	// Tạo phiên
	sess, err := sessionManager.CreateSession(sessionID, conn)
	if err != nil {
		logger.Errorf("Failed to create session, session_id=%s, error=%v", sessionID, err)
		conn.Close()
		return
	}

	defer func() {
		sessionManager.RemoveSession(sessionID)
		logger.Infof("WebSocket connection closed, session_id=%s", sessionID)
	}()

	logger.Infof("New WebSocket connection established, session_id=%s", sessionID)

	// Gửi xác nhận kết nối
	if sess != nil {
		select {
		case sess.SendQueue <- map[string]interface{}{
			"type":       "connection",
			"message":    "WebSocket connected, ready for audio",
			"session_id": sessionID,
		}:
		default:
			logger.Warnf("Session send queue is full, dropping connection confirmation")
		}
	}

	// Xử lý tin nhắn
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			logger.Warnf("WebSocket read error")
			break
		}

		// Thời gian chờ đọc được làm mới mỗi khi nhận được tin nhắn.
		if wsConfig.ReadTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(time.Duration(wsConfig.ReadTimeout) * time.Second))
		}

		// Kiểm tra kích thước tin nhắn
		if wsConfig.MaxMessageSize > 0 && len(message) > wsConfig.MaxMessageSize {
			logger.Warnf("Message too large, closing connection")
			break
		}

		// Xử lý dữ liệu âm thanh
		if len(message) > 0 {
			if err := sessionManager.ProcessAudioData(sessionID, message); err != nil {
				logger.Errorf("Failed to process audio data, session_id=%s, error=%v", sessionID, err)
				// Gửi thông báo lỗi qua SendQueue của phiên
				if sess != nil {
					select {
					case sess.SendQueue <- map[string]interface{}{
						"type":    "error",
						"message": err.Error(),
					}:
					default:
						logger.Warnf("Session send queue is full, dropping error message")
					}
				}
			}
		}
	}
}
