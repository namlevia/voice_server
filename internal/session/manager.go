package session

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"voice_server/config"
	"voice_server/internal/logger"
	"voice_server/internal/pool"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Phiên WebSocket phiên
type Session struct {
	ID          string
	Conn        *websocket.Conn
	VADInstance pool.VADInstanceInterface // Sử dụng giao diện phiên bản VAD
	LastSeen    int64                     // Sử dụng int64 để lưu trữ dấu thời gian
	mu          sync.RWMutex
	closed      int32

	// Gửi hàng đợi và kênh
	SendQueue    chan interface{}
	sendDone     chan struct{}
	sendErrCount int32

	// Phát hiện sự sống
	lastActivity time.Time

	// liên quan đến mười-vad
	isInSpeech        bool
	currentSegment    []float32
	silenceFrameCount int
}

// Trình quản lý phiên quản lý
type Manager struct {
	sessions   map[string]*Session
	recognizer *sherpa.OfflineRecognizer
	vadPool    pool.VADPoolInterface
	mu         sync.RWMutex

	// Thống kê
	totalSessions  int64
	activeSessions int64
	totalMessages  int64

	// dọn dẹp
	ctx    context.Context
	cancel context.CancelFunc
}

// Vùng đệm toàn cầu (8KB)
var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 8192)
	},
}

// Nhóm lát float32 toàn cầu (hỗ trợ tối đa 8KB/2=4096 điểm lấy mẫu)
var float32Pool = sync.Pool{}

func getFloat32PoolSlice() []float32 {
	chunkSize := config.GlobalConfig.Audio.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 4096
	}
	return make([]float32, chunkSize)
}

// NewManager tạo trình quản lý phiên mới
func NewManager(recognizer *sherpa.OfflineRecognizer, vadPool pool.VADPoolInterface) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	manager := &Manager{
		sessions:   make(map[string]*Session),
		recognizer: recognizer,
		vadPool:    vadPool,
		ctx:        ctx,
		cancel:     cancel,
	}

	return manager
}

// CreateSession tạo một phiên mới
func (m *Manager) CreateSession(sessionID string, conn *websocket.Conn) (*Session, error) {
	// Phiên bản VAD không được phân bổ ở đây, VADInstance được khởi tạo thành 0
	if m.vadPool == nil {
		return nil, fmt.Errorf("VAD pool is not initialized")
	}

	session := &Session{
		ID:                sessionID,
		Conn:              conn,
		VADInstance:       nil, // phân bổ chậm trễ
		LastSeen:          time.Now().UnixNano(),
		closed:            0,
		SendQueue:         make(chan interface{}, config.GlobalConfig.Session.SendQueueSize),
		sendDone:          make(chan struct{}),
		sendErrCount:      0,
		lastActivity:      time.Now(),
		isInSpeech:        false,
		currentSegment:    nil,
		silenceFrameCount: 0,
	}

	// Bắt đầu gửi coroutine
	go session.sendLoop()

	m.mu.Lock()
	m.sessions[sessionID] = session
	m.mu.Unlock()

	atomic.AddInt64(&m.totalSessions, 1)
	atomic.AddInt64(&m.activeSessions, 1)

	return session, nil
}

// GetSession nhận phiên
func (m *Manager) GetSession(sessionID string) (*Session, bool) {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if exists {
		// Cập nhật LastSeen bằng các thao tác nguyên tử
		atomic.StoreInt64(&session.LastSeen, time.Now().UnixNano())
	}

	return session, exists
}

// RemoveSessionXóa phiên
func (m *Manager) RemoveSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, exists := m.sessions[sessionID]; exists {
		m.closeSession(session)
		delete(m.sessions, sessionID)
		atomic.AddInt64(&m.activeSessions, -1)
		logger.Infof("🗑️  Session removed")
	}
}

// vòng lặp gửi sendLoop
func (s *Session) sendLoop() {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("❌ Send loop panicked for session %s: %v", s.ID, r)
		}
	}()

	for {
		select {
		case msg := <-s.SendQueue:
			if atomic.LoadInt32(&s.closed) == 1 {
				return
			}

			// Viết tin nhắn trực tiếp mà không đặt thời gian chờ ghi
			if err := s.Conn.WriteJSON(msg); err != nil {
				atomic.AddInt32(&s.sendErrCount, 1)
				logger.Errorf("Failed to send message to session %s: %v", s.ID, err)
				// Nếu các lỗi liên tiếp vượt quá ngưỡng, hãy đóng phiên
				if atomic.LoadInt32(&s.sendErrCount) > int32(config.GlobalConfig.Session.MaxSendErrors) {
					logger.Errorf("Too many send errors for session, closing")
					atomic.StoreInt32(&s.closed, 1)
					return
				}
			} else {
				atomic.StoreInt32(&s.sendErrCount, 0)
			}
		case <-s.sendDone:
			return
		}
	}
}

// ProcessAudioData xử lý dữ liệu âm thanh
func (m *Manager) ProcessAudioData(sessionID string, audioData []byte) error {
	session, exists := m.GetSession(sessionID)
	if !exists {
		logger.Errorf("Session %s not found when processing audio data", sessionID)
		return fmt.Errorf("session %s not found", sessionID)
	}

	if atomic.LoadInt32(&session.closed) == 1 {
		logger.Errorf("Session %s is closed, cannot process audio data", sessionID)
		return fmt.Errorf("session %s is closed", sessionID)
	}

	// Kiểm tra và trì hoãn việc phân bổ các phiên bản VAD
	if session.VADInstance == nil {
		vadInstance, err := m.vadPool.Get()
		if err != nil {
			logger.Errorf("Failed to get VAD instance for session %s: %v", sessionID, err)
			return fmt.Errorf("failed to get VAD instance for session %s: %v", sessionID, err)
		}
		session.VADInstance = vadInstance
		logger.Infof("✅ Session %s assigned %s VAD instance %d", sessionID, vadInstance.GetType(), vadInstance.GetID())
	}

	// Cập nhật thời gian hoạt động của phiên
	atomic.StoreInt64(&session.LastSeen, time.Now().UnixNano())
	atomic.AddInt64(&m.totalMessages, 1)

	// Xác thực dữ liệu đầu vào
	if len(audioData) == 0 {
		logger.Warnf("Session %s: Received empty audio data", sessionID)
		return fmt.Errorf("empty audio data")
	}

	if len(audioData)%2 != 0 {
		logger.Warnf("Session %s: Audio data length %d is not even (expecting 16-bit samples)", sessionID, len(audioData))
		return fmt.Errorf("invalid audio data length: %d", len(audioData))
	}

	// Chuyển đổi dữ liệu âm thanh
	numSamples := len(audioData) / 2
	samples := float32Pool.Get()
	var float32Slice []float32
	if samples == nil {
		float32Slice = getFloat32PoolSlice()
	} else {
		float32Slice = samples.([]float32)
	}
	if cap(float32Slice) < numSamples {
		float32Slice = make([]float32, numSamples)
	}
	float32Slice = float32Slice[:numSamples]
	defer float32Pool.Put(float32Slice)
	normalizeFactor := config.GlobalConfig.Audio.NormalizeFactor
	for i := 0; i < numSamples; i++ {
		sample := int16(audioData[i*2]) | int16(audioData[i*2+1])<<8
		float32Slice[i] = float32(sample) / normalizeFactor
	}

	logger.Debugf("Session %s: Converted %d bytes to %d float32 samples", sessionID, len(audioData), numSamples)

	// Xử lý theo loại VAD
	switch session.VADInstance.GetType() {
	case pool.SILERO_TYPE:
		return m.processSileroVAD(session, sessionID, float32Slice)
	case pool.TEN_VAD_TYPE:
		return m.processTenVAD(session, sessionID, float32Slice)
	default:
		return fmt.Errorf("unsupported VAD type: %s", session.VADInstance.GetType())
	}
}

// quy trìnhSileroVAD Quy trình Silero VAD
func (m *Manager) processSileroVAD(session *Session, sessionID string, float32Slice []float32) error {
	// Nhập xác nhận để lấy phiên bản Silero VAD
	sileroInstance, ok := session.VADInstance.(*pool.SileroVADInstance)
	if !ok {
		return fmt.Errorf("invalid Silero VAD instance type")
	}

	// Phát hiện VAD - sử dụng cấu hình thời gian chờ phản hồi
	vadTimeout := time.Duration(config.GlobalConfig.Response.Timeout) * time.Second
	vadCtx, vadCancel := context.WithTimeout(context.Background(), vadTimeout)
	defer vadCancel()

	// Thực hiện xử lý VAD trong goroutine để tránh bị chặn
	vadDone := make(chan struct{})
	go func() {
		defer close(vadDone)
		sileroInstance.VAD.AcceptWaveform(float32Slice)
	}()

	// Đang chờ quá trình xử lý VAD hoàn tất hoặc hết thời gian chờ
	select {
	case <-vadDone:
		// Quá trình xử lý VAD đã hoàn tất
	case <-vadCtx.Done():
		logger.Warnf("Session %s: VAD processing timeout", sessionID)
		return fmt.Errorf("VAD processing timeout")
	}

	// Xử lý các phân đoạn lời nói
	segmentCount := 0
	var speechSegments [][]float32
	sampleRate := config.GlobalConfig.Audio.SampleRate

	// Thu thập tất cả các đoạn lời nói hợp lệ
	for !sileroInstance.VAD.IsEmpty() {
		segment := sileroInstance.VAD.Front()
		sileroInstance.VAD.Pop()
		segmentCount++

		if segment != nil && len(segment.Samples) > 0 {
			// Kiểm tra lại trạng thái phiên
			if atomic.LoadInt32(&session.closed) == 1 {
				logger.Warnf("Session %s closed during speech segment processing", sessionID)
				return fmt.Errorf("session %s closed during processing", sessionID)
			}

			// Xác minh dữ liệu âm thanh
			if len(segment.Samples) == 0 {
				logger.Warnf("Session %s: Speech segment %d has no samples", sessionID, segmentCount)
				continue
			}

			// Kiểm tra thời lượng âm thanh
			duration := float64(len(segment.Samples)) / float64(sampleRate)
			minSpeechDuration := float64(config.GlobalConfig.VAD.SileroVAD.MinSpeechDuration)
			if duration < minSpeechDuration {
				logger.Debugf("Session %s: Skipping short segment %d (%.2fs < %.2fs)", sessionID, segmentCount, duration, minSpeechDuration)
				continue
			}

			// Kiểm tra thời lượng tối đa
			maxDuration := float64(config.GlobalConfig.VAD.SileroVAD.MaxSpeechDuration)
			if duration > maxDuration {
				logger.Warnf("Session %s: Segment %d too long (%.2fs > %.2fs), truncating", sessionID, segmentCount, duration, maxDuration)
				maxSamples := int(maxDuration * float64(sampleRate))
				segment.Samples = segment.Samples[:maxSamples]
			}

			speechSegments = append(speechSegments, segment.Samples)
			logger.Debugf("Session %s: Collected segment %d with %d samples (%.2fs)", sessionID, segmentCount, len(segment.Samples), duration)
		} else {
			logger.Warnf("Session %s: Empty or null speech segment %d", sessionID, segmentCount)
		}
	}

	// Xử lý các đoạn giọng nói được thu thập
	for i, samples := range speechSegments {
		// Gửi nhiệm vụ công nhận
		taskID := fmt.Sprintf("%s_%d_%d", sessionID, time.Now().UnixNano(), i)
		go func(samples []float32, sampleRate int, sessionID string, taskID string) {
			stream := sherpa.NewOfflineStream(m.recognizer)
			defer sherpa.DeleteOfflineStream(stream)
			stream.AcceptWaveform(sampleRate, samples)
			m.recognizer.Decode(stream)
			result := stream.GetResult()
			if result != nil {
				m.handleRecognitionResult(sessionID, result.Text, nil)
			} else {
				m.handleRecognitionResult(sessionID, "", fmt.Errorf("recognition failed"))
			}
		}(samples, sampleRate, sessionID, taskID)
	}

	return nil
}

// quy trìnhTenVAD xử lý TEN-VAD
func (m *Manager) processTenVAD(session *Session, sessionID string, float32Slice []float32) error {
	// Xác nhận kiểu nhận phiên bản TEN-VAD
	tenVADInstance, ok := session.VADInstance.(*pool.TenVADInstance)
	if !ok {
		return fmt.Errorf("invalid TEN-VAD instance type")
	}

	hopSize := config.GlobalConfig.VAD.TenVAD.HopSize
	minSpeechFrames := config.GlobalConfig.VAD.TenVAD.MinSpeechFrames
	maxSilenceFrames := config.GlobalConfig.VAD.TenVAD.MaxSilenceFrames

	// đóng khung
	for i := 0; i < len(float32Slice); i += hopSize {
		end := i + hopSize
		if end > len(float32Slice) {
			end = len(float32Slice)
		}
		frame := float32Slice[i:end]
		int16Frame := make([]int16, len(frame))
		for j, f := range frame {
			int16Frame[j] = int16(f * 32768)
		}
		_, flag, err := pool.GetInstance().ProcessAudio(tenVADInstance.Handle, int16Frame)
		if err != nil {
			return fmt.Errorf("TEN-VAD ProcessAudio error: %v", err)
		}

		if flag == 1 {
			if !session.isInSpeech {
				logger.Debugf("Session %s: Speech started", sessionID)
				session.isInSpeech = true
				session.currentSegment = make([]float32, 0)
				session.silenceFrameCount = 0
			}
			session.currentSegment = append(session.currentSegment, frame...)
			session.silenceFrameCount = 0 // Đặt lại số lần im lặng
		} else {
			if session.isInSpeech {
				session.silenceFrameCount++
				session.currentSegment = append(session.currentSegment, frame...)
				if session.silenceFrameCount >= maxSilenceFrames {
					frameCount := len(session.currentSegment) / hopSize
					if frameCount >= minSpeechFrames {
						logger.Debugf("Session %s: Speech segment completed with %d samples (%d frames)", sessionID, len(session.currentSegment), frameCount)
						duration := float64(len(session.currentSegment)) / float64(config.GlobalConfig.Audio.SampleRate)
						logger.Infof("ASR segment length: %.2fs, samples: %d", duration, len(session.currentSegment))
						taskID := fmt.Sprintf("%s_%d", sessionID, time.Now().UnixNano())
						segmentCopy := make([]float32, len(session.currentSegment))
						copy(segmentCopy, session.currentSegment)
						go func(segment []float32, sessionID string, taskID string) {
							stream := sherpa.NewOfflineStream(m.recognizer)
							defer sherpa.DeleteOfflineStream(stream)
							stream.AcceptWaveform(config.GlobalConfig.Audio.SampleRate, segment)
							m.recognizer.Decode(stream)
							result := stream.GetResult()
							if result != nil {
								m.handleRecognitionResult(sessionID, result.Text, nil)
							} else {
								m.handleRecognitionResult(sessionID, "", fmt.Errorf("recognition failed"))
							}
						}(segmentCopy, sessionID, taskID)
					} else {
						logger.Debugf("Session %s: Speech segment too short (%d frames), discarding", sessionID, frameCount)
					}
					session.isInSpeech = false
					session.silenceFrameCount = 0
					session.currentSegment = nil
				}
			}
		}
	}

	return nil
}

// handRecognitionResult xử lý kết quả nhận dạng
func (m *Manager) handleRecognitionResult(sessionID, result string, err error) {
	session, exists := m.GetSession(sessionID)
	if !exists {
		logger.Warnf("Session %s not found when handling recognition result, session may have been closed", sessionID)
		return
	}

	// Kiểm tra xem phiên đã được đóng chưa
	if atomic.LoadInt32(&session.closed) == 1 {
		logger.Warnf("Session %s is closed when handling recognition result", sessionID)
		return
	}

	// Kết quả nhận dạng chỉ được trả về khi err bằng 0 và kết quả không trống.
	if err == nil && len(result) > 0 {
		response := map[string]interface{}{
			"type":      "final",
			"text":      result,
			"timestamp": time.Now().UnixMilli(),
		}
		select {
		case session.SendQueue <- response:
			logger.Infof("Recognition result queued for session %s: %s", sessionID, result)
		default:
			logger.Warnf("Session %s send queue is full, dropping recognition result", sessionID)
		}
		return
	}

	// Log khi có lỗi nhưng không trả lại cho người dùng
	if err != nil {
		logger.Errorf("Recognition error for session %s: %v", sessionID, err)
	}
	// Trong các trường hợp khác (chẳng hạn như lỗi nhận dạng, lỗi hoặc kết quả trống) không có gì được trả về
}

// closeSession đóng phiên
func (m *Manager) closeSession(session *Session) {
	if atomic.CompareAndSwapInt32(&session.closed, 0, 1) {
		// Đóng kênh gửi
		close(session.sendDone)
		// Xóa hàng đợi gửi
		for len(session.SendQueue) > 0 {
			<-session.SendQueue
		}

		// Trả lại phiên bản VAD về nhóm
		if session.VADInstance != nil && m.vadPool != nil {
			m.vadPool.Put(session.VADInstance)
			session.VADInstance = nil
			logger.Infof("🔄 Returned VAD instance to pool for session %s", session.ID)
		}

		if session.Conn != nil {
			session.Conn.Close()
		}
	}
}

// GetStats Nhận số liệu thống kê của người quản lý - phiên bản nâng cao
func (m *Manager) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Nhận số liệu thống kê về nhóm tài nguyên
	var poolStats map[string]interface{}
	if m.vadPool != nil {
		poolStats = m.vadPool.GetStats()
	} else {
		poolStats = map[string]interface{}{"status": "not_initialized"}
	}

	return map[string]interface{}{
		"total_sessions":   atomic.LoadInt64(&m.totalSessions),
		"active_sessions":  atomic.LoadInt64(&m.activeSessions),
		"total_messages":   atomic.LoadInt64(&m.totalMessages),
		"current_sessions": len(m.sessions),
		"pool_stats":       poolStats,
	}
}

// Tắt máy đóng trình quản lý
func (m *Manager) Shutdown() {
	logger.Infof("🛑 Shutting down session manager...")

	// Hủy ngữ cảnh
	m.cancel()

	// Đóng tất cả các phiên
	m.mu.Lock()
	for sessionID, session := range m.sessions {
		logger.Infof("🛑 Closing session: %s", sessionID)
		m.closeSession(session)
	}
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()

	logger.Infof("✅ Session manager shutdown complete")
}
