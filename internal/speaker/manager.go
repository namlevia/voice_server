package speaker

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"voice_server/config"
	"voice_server/internal/logger"
	"voice_server/internal/pool"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Trình quản lý Trình quản lý nhận dạng giọng nói
type Manager struct {
	extractor    *sherpa.SpeakerEmbeddingExtractor
	threshold    float32
	embeddingDim int
	dataDir      string

	// Máy khách cơ sở dữ liệu vectơ (hỗ trợ nhiều chương trình phụ trợ: JSON, Qdrant)
	vectorDB      VectorDatabase
	vectorDBMutex sync.RWMutex

	// Nhóm VAD (để lọc sự im lặng)
	vadPool pool.VADPoolInterface
}

// Cấu hình cấu hình nhận dạng giọng nói
type Config struct {
	ModelPath  string  `json:"model_path"`
	NumThreads int     `json:"num_threads"`
	Provider   string  `json:"provider"`
	Threshold  float32 `json:"threshold"`
	DataDir    string  `json:"data_dir"` // Dành riêng cho các mục đích sử dụng khác (chẳng hạn như các tệp tạm thời)

	// Loại bộ nhớ: "json" hoặc "qdrant" (mặc định: "json")
	StorageType string `json:"storage_type"`

	// Cấu hình lưu trữ JSON
	JSONStorage struct {
		FilePath string `json:"file_path"` // Đường dẫn tệp JSON
	} `json:"json_storage"`

	// Cấu hình lưu trữ Qdrant
	Qdrant struct {
		Host           string `json:"host"`            // Địa chỉ Qdrant, localhost mặc định
		Port           int    `json:"port"`            // Cổng Qdrant, mặc định 6334
		CollectionName string `json:"collection_name"` // Tên bộ sưu tập, loa_embeddings mặc định
	} `json:"qdrant"`
}

// NewManager tạo trình quản lý nhận dạng giọng nói
func NewManager(config *Config, vadPool pool.VADPoolInterface) (*Manager, error) {
	// Đảm bảo thư mục dữ liệu tồn tại (cho các mục đích khác như tệp tạm thời)
	if config.DataDir != "" {
		if err := os.MkdirAll(config.DataDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create data directory: %v", err)
		}
	}

	// Tạo cấu hình trích xuất tính năng giọng nói
	extractorConfig := &sherpa.SpeakerEmbeddingExtractorConfig{
		Model:      config.ModelPath,
		NumThreads: config.NumThreads,
		Debug:      0,
		Provider:   config.Provider,
	}

	// Tạo trình trích xuất tính năng giọng nói
	extractor := sherpa.NewSpeakerEmbeddingExtractor(extractorConfig)
	if extractor == nil {
		return nil, fmt.Errorf("failed to create speaker embedding extractor")
	}

	// Nhận kích thước tính năng
	dim := extractor.Dim()
	logger.Infof("Speaker embedding dimension: %d", dim)

	// Chọn phụ trợ lưu trữ dựa trên cấu hình
	var vectorDB VectorDatabase

	// Sử dụng json theo mặc định
	storageType := config.StorageType
	if storageType == "" {
		storageType = "json"
	}

	switch storageType {
	case "json":
		// Lưu trữ tệp JSON
		jsonFilePath := config.JSONStorage.FilePath
		if jsonFilePath == "" {
			jsonFilePath = filepath.Join(config.DataDir, "speaker_embeddings.json")
		}

		jsonDB, err := NewJSONVectorDB(&JSONVectorDBConfig{
			FilePath:     jsonFilePath,
			EmbeddingDim: dim,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize JSON vector database: %v", err)
		}
		vectorDB = jsonDB
		logger.Infof("✅ Using JSON storage: %s", jsonFilePath)

	case "qdrant":
		// Cơ sở dữ liệu vectơ Qdrant
		qdrantConfig := &QdrantConfig{
			Host:           config.Qdrant.Host,
			Port:           config.Qdrant.Port,
			CollectionName: config.Qdrant.CollectionName,
		}

		// Đặt giá trị mặc định
		if qdrantConfig.Host == "" {
			qdrantConfig.Host = "localhost"
		}
		if qdrantConfig.Port == 0 {
			qdrantConfig.Port = 6334
		}
		if qdrantConfig.CollectionName == "" {
			qdrantConfig.CollectionName = "speaker_embeddings"
		}

		qdrantDB, err := NewQdrantVectorDB(qdrantConfig, dim)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize Qdrant vector database: %v", err)
		}

		// Khởi tạo Qdrant (đảm bảo Bộ sưu tập tồn tại)
		if err := qdrantDB.Init(); err != nil {
			return nil, fmt.Errorf("failed to initialize Qdrant: %v", err)
		}

		vectorDB = qdrantDB
		logger.Infof("✅ Using Qdrant storage: %s:%d, collection: %s",
			qdrantConfig.Host, qdrantConfig.Port, qdrantConfig.CollectionName)

	default:
		return nil, fmt.Errorf("unknown storage type: %s (supported: json, qdrant)", config.StorageType)
	}

	manager := &Manager{
		extractor:    extractor,
		threshold:    config.Threshold,
		embeddingDim: dim,
		dataDir:      config.DataDir,
		vectorDB:     vectorDB,
		vadPool:      vadPool,
	}

	logger.Infof("✅ Speaker Manager initialized with storage type: %s", storageType)
	return manager, nil
}

// Đóng đóng trình quản lý và giải phóng tài nguyên
func (m *Manager) Close() {
	// Đóng kết nối cơ sở dữ liệu vector
	if m.vectorDB != nil {
		m.vectorDB.Close()
	}

	// giải nén phát hành
	if m.extractor != nil {
		sherpa.DeleteSpeakerEmbeddingExtractor(m.extractor)
	}

	logger.Infof("Speaker Manager closed, all resources released")
}

// extractEmbedding trích xuất các tính năng giọng nói từ dữ liệu âm thanh (phương pháp riêng tư)
func (m *Manager) extractEmbedding(audioData []float32, sampleRate int) ([]float32, error) {
	// Tạo luồng âm thanh
	stream := m.extractor.CreateStream()
	defer sherpa.DeleteOnlineStream(stream)

	// Chấp nhận dữ liệu âm thanh
	stream.AcceptWaveform(sampleRate, audioData)
	stream.InputFinished()

	// Kiểm tra xem nó đã sẵn sàng chưa
	if !m.extractor.IsReady(stream) {
		return nil, fmt.Errorf("insufficient audio data for embedding extraction")
	}

	// Trích xuất tính năng
	embedding := m.extractor.Compute(stream)
	if len(embedding) == 0 {
		return nil, fmt.Errorf("failed to extract embedding")
	}

	// LƯU Ý: Không cần chuẩn hóa vectơ theo cách thủ công
	// Qdrant sẽ tự động chuẩn hóa các vectơ khi sử dụng Distance_Cosine (cả được lưu trữ và truy vấn tự động)
	// Điều này đảm bảo tính nhất quán trong việc lưu trữ và tìm kiếm vectơ, đồng thời cải thiện hiệu quả tìm kiếm

	return embedding, nil
}

// filterSilenceWithVAD Sử dụng TEN-VAD để lọc khoảng lặng và chỉ giữ lại các đoạn giọng nói
func (m *Manager) filterSilenceWithVAD(audioData []float32, sampleRate int) ([]float32, error) {
	if m.vadPool == nil {
		return audioData, nil
	}

	// Nhận phiên bản VAD
	vadInstance, err := m.vadPool.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get VAD instance: %v", err)
	}
	defer m.vadPool.Put(vadInstance)

	// Nhập xác nhận để đảm bảo đó là phiên bản TEN-VAD
	tenVADInstance, ok := vadInstance.(*pool.TenVADInstance)
	if !ok {
		return nil, fmt.Errorf("VAD instance is not TEN-VAD type")
	}

	hopSize := config.GlobalConfig.VAD.TenVAD.HopSize
	var filteredAudio []float32

	// Đóng khung âm thanh
	for i := 0; i < len(audioData); i += hopSize {
		end := i + hopSize
		if end > len(audioData) {
			end = len(audioData)
		}
		frame := audioData[i:end]

		// Chuyển đổi float32 sang int16
		int16Frame := make([]int16, len(frame))
		for j, f := range frame {
			// Giới hạn phạm vi ở [-1.0, 1.0] và sau đó chuyển đổi thành int16
			if f > 1.0 {
				f = 1.0
			} else if f < -1.0 {
				f = -1.0
			}
			int16Frame[j] = int16(f * 32768)
		}

		// Gọi xử lý VAD
		_, flag, err := pool.GetInstance().ProcessAudio(tenVADInstance.Handle, int16Frame)
		if err != nil {
			return nil, fmt.Errorf("TEN-VAD ProcessAudio error: %v", err)
		}

		// flag == 1 nghĩa là lời nói, giữ nguyên khung; cờ == 0 có nghĩa là im lặng, loại bỏ nó
		if flag == 1 {
			filteredAudio = append(filteredAudio, frame...)
		}
	}

	logger.Debugf("VAD filtering: original %d samples, filtered %d samples (removed %.2f%%)",
		len(audioData), len(filteredAudio),
		float64(len(audioData)-len(filteredAudio))/float64(len(audioData))*100)

	return filteredAudio, nil
}

// filterSilenceWithVADKeepEdges sử dụng TEN-VAD để lọc khoảng lặng và giữ lại khoảng lặng 100ms trước và sau
func (m *Manager) filterSilenceWithVADKeepEdges(audioData []float32, sampleRate int) ([]float32, error) {
	if m.vadPool == nil {
		return audioData, nil
	}

	// Nhận phiên bản VAD
	vadInstance, err := m.vadPool.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get VAD instance: %v", err)
	}
	defer m.vadPool.Put(vadInstance)

	// Nhập xác nhận để đảm bảo đó là phiên bản TEN-VAD
	tenVADInstance, ok := vadInstance.(*pool.TenVADInstance)
	if !ok {
		return nil, fmt.Errorf("VAD instance is not TEN-VAD type")
	}

	hopSize := config.GlobalConfig.VAD.TenVAD.HopSize

	// Tính số điểm lấy mẫu tương ứng với 100ms
	silenceSamples := int(float64(sampleRate) * 0.1) // 100ms = 0,1 giây

	// Ghi lại kết quả và vị trí VAD cho từng khung hình
	type frameInfo struct {
		startIdx int
		endIdx   int
		isSpeech bool
	}

	var frames []frameInfo

	// Xử lý âm thanh theo khung và ghi lại kết quả VAD của từng khung
	for i := 0; i < len(audioData); i += hopSize {
		end := i + hopSize
		if end > len(audioData) {
			end = len(audioData)
		}
		frame := audioData[i:end]

		// Chuyển đổi float32 sang int16
		int16Frame := make([]int16, len(frame))
		for j, f := range frame {
			// Giới hạn phạm vi ở [-1.0, 1.0] và sau đó chuyển đổi thành int16
			if f > 1.0 {
				f = 1.0
			} else if f < -1.0 {
				f = -1.0
			}
			int16Frame[j] = int16(f * 32768)
		}

		// Gọi xử lý VAD
		_, flag, err := pool.GetInstance().ProcessAudio(tenVADInstance.Handle, int16Frame)
		if err != nil {
			return nil, fmt.Errorf("TEN-VAD ProcessAudio error: %v", err)
		}

		// cờ == 1 nghĩa là phát biểu, cờ == 0 nghĩa là im lặng
		frames = append(frames, frameInfo{
			startIdx: i,
			endIdx:   end,
			isSpeech: flag == 1,
		})
	}

	// Tìm vị trí của khung lời nói đầu tiên và cuối cùng
	firstSpeechIdx := -1
	lastSpeechIdx := -1
	for i, frame := range frames {
		if frame.isSpeech {
			if firstSpeechIdx == -1 {
				firstSpeechIdx = i
			}
			lastSpeechIdx = i
		}
	}

	// Nếu không tìm thấy khung giọng nói, trả về trống
	if firstSpeechIdx == -1 {
		logger.Debugf("VAD filtering: no speech detected, returning empty audio")
		return []float32{}, nil
	}

	// Tính toán vị trí bắt đầu và kết thúc của việc lưu giữ
	// Vị trí bắt đầu của khung lời nói đầu tiên trừ 100ms
	startIdx := frames[firstSpeechIdx].startIdx - silenceSamples
	if startIdx < 0 {
		startIdx = 0
	}

	// Vị trí kết thúc của khung lời nói cuối cùng cộng thêm 100ms
	endIdx := frames[lastSpeechIdx].endIdx + silenceSamples
	if endIdx > len(audioData) {
		endIdx = len(audioData)
	}

	// Trích xuất các đoạn âm thanh được giữ lại
	filteredAudio := audioData[startIdx:endIdx]

	logger.Debugf("VAD filtering with edges: original %d samples, filtered %d samples (kept %.2f%%, first speech at %d, last speech at %d)",
		len(audioData), len(filteredAudio),
		float64(len(filteredAudio))/float64(len(audioData))*100,
		frames[firstSpeechIdx].startIdx, frames[lastSpeechIdx].endIdx)

	return filteredAudio, nil
}

// FilterSilenceWithVADKeepEdges sử dụng TEN-VAD để lọc khoảng lặng và giữ lại khoảng im lặng 100ms trước và sau (phương thức công khai)
func (m *Manager) FilterSilenceWithVADKeepEdges(audioData []float32, sampleRate int) ([]float32, error) {
	return m.filterSilenceWithVADKeepEdges(audioData, sampleRate)
}

// ExtractEmbedding trích xuất các tính năng giọng nói từ dữ liệu âm thanh (phương thức công khai cho các cuộc gọi bên ngoài)
func (m *Manager) ExtractEmbedding(audioData []float32, sampleRate int) ([]float32, error) {
	return m.extractEmbedding(audioData, sampleRate)
}

// GetEmbeddingDim lấy kích thước nhúng
func (m *Manager) GetEmbeddingDim() int {
	return m.embeddingDim
}

// RegisterSpeaker đăng ký giọng nói (hỗ trợ cách ly kích thước UID và ID tác nhân)
func (m *Manager) RegisterSpeaker(uid, agentID, speakerID, speakerName, uuid string, audioData []float32, sampleRate int) error {
	if uid == "" {
		return fmt.Errorf("uid is required")
	}

	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}

	if uuid == "" {
		return fmt.Errorf("uuid is required")
	}

	// Lưu ý: Dữ liệu âm thanh phải được lọc và tắt tiếng trước khi gọi phương thức này (100ms trước và sau khi được giữ lại)
	// Trích xuất các tính năng giọng nói
	embedding, err := m.extractEmbedding(audioData, sampleRate)
	if err != nil {
		return fmt.Errorf("failed to extract embedding: %v", err)
	}

	// Xác minh kích thước vectơ nhúng
	if len(embedding) != m.embeddingDim {
		return fmt.Errorf("embedding dimension mismatch: expected %d, got %d", m.embeddingDim, len(embedding))
	}

	// Truy vấn số lượng mẫu hiện có cho loa này (dùng để xác định sample_index)
	sampleIndex, err := m.vectorDB.GetSpeakerSampleCount(uid, agentID, speakerID)
	if err != nil {
		// Nếu truy vấn không thành công, có thể loa không tồn tại và bắt đầu từ 0
		sampleIndex = 0
	}

	// Chèn vào cơ sở dữ liệu vector Qdrant
	now := time.Now().Unix()
	err = m.vectorDB.Insert(uid, agentID, speakerID, speakerName, uuid, embedding, sampleIndex, now, now)
	if err != nil {
		return fmt.Errorf("failed to insert to vector database: %v", err)
	}

	logger.Infof("Successfully registered speaker %s (%s) for uid %s, agent_id %s, uuid %s, sample index: %d",
		speakerID, speakerName, uid, agentID, uuid, sampleIndex)
	return nil
}

// Xác địnhSpeaker xác định dấu giọng nói (hỗ trợ lọc UID, Agent_id, loa_id và loa_name tùy chọn)
// uid: ID người dùng, nếu là chuỗi rỗng thì sẽ không được dùng làm điều kiện lọc
// ID đại lý: ID đại lý. Nếu là chuỗi rỗng thì nó sẽ không được sử dụng làm điều kiện lọc.
// loaID: ID loa, nếu là chuỗi trống thì sẽ không được dùng làm điều kiện lọc
// loaName: tên loa, nếu là chuỗi rỗng thì sẽ không được dùng làm điều kiện lọc
// ngưỡng: ngưỡng nhận dạng, nếu <= 0 thì sử dụng ngưỡng mặc định
func (m *Manager) IdentifySpeaker(uid, agentID, speakerID, speakerName string, audioData []float32, sampleRate int, threshold ...float32) (*IdentifyResult, error) {
	// Xác định ngưỡng sẽ sử dụng: nếu ngưỡng hợp lệ (>0) được chuyển vào thì giá trị được chuyển vào sẽ được sử dụng; nếu không thì ngưỡng mặc định sẽ được sử dụng
	useThreshold := m.threshold
	if len(threshold) > 0 && threshold[0] > 0 {
		useThreshold = threshold[0]
	}

	// Trích xuất các tính năng giọng nói
	embedding, err := m.extractEmbedding(audioData, sampleRate)
	if err != nil {
		return nil, fmt.Errorf("failed to extract embedding: %v", err)
	}

	// Tìm kiếm trong cơ sở dữ liệu vectơ Qdrant (được lọc theo UID tùy chọn, Agent_id, loa_id và loa_name, trả về top 1)
	results, err := m.vectorDB.SearchWithOptionalFilters(uid, agentID, speakerID, speakerName, embedding, useThreshold, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to search in vector database: %v", err)
	}

	result := &IdentifyResult{
		Identified:  false,
		SpeakerID:   "",
		SpeakerName: "",
		Confidence:  0.0,
		Threshold:   useThreshold,
	}

	if len(results) > 0 {
		bestMatch := results[0]
		result.Identified = true
		result.SpeakerID = bestMatch.SpeakerID
		result.SpeakerName = bestMatch.SpeakerName
		result.Confidence = bestMatch.Confidence
	}

	return result, nil
}

// VerifySpeaker xác minh giọng nói (hỗ trợ cách ly kích thước UID và ID tác nhân)
func (m *Manager) VerifySpeaker(uid, agentID, speakerID string, audioData []float32, sampleRate int) (*VerifyResult, error) {
	if uid == "" {
		return nil, fmt.Errorf("uid is required")
	}

	// Trích xuất các tính năng giọng nói
	embedding, err := m.extractEmbedding(audioData, sampleRate)
	if err != nil {
		return nil, fmt.Errorf("failed to extract embedding: %v", err)
	}

	// Tìm kiếm tất cả các mẫu cho loa này trong Qdrant
	// Filter: uid = xxx AND agent_id = xxx AND speaker_id = xxx
	results, err := m.vectorDB.SearchWithFilter(uid, agentID, speakerID, embedding, m.threshold, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to search in vector database: %v", err)
	}

	verified := len(results) > 0
	confidence := float32(0.0)
	speakerName := ""

	if verified {
		confidence = results[0].Confidence
		speakerName = results[0].SpeakerName
	} else {
		// Nếu không tìm thấy, hãy thử lấy thông tin người nói (xác minh nếu nó tồn tại)
		speakerInfo, err := m.vectorDB.GetSpeakerInfo(uid, agentID, speakerID)
		if err != nil {
			return nil, fmt.Errorf("speaker %s not found", speakerID)
		}
		speakerName = speakerInfo.Name
	}

	return &VerifyResult{
		SpeakerID:   speakerID,
		SpeakerName: speakerName,
		Verified:    verified,
		Confidence:  confidence,
		Threshold:   m.threshold,
	}, nil
}

// GetAllSpeakers Nhận tất cả người phát biểu đã đăng ký cho UID và ID tác nhân được chỉ định
func (m *Manager) GetAllSpeakers(uid, agentID string) []*SpeakerInfo {
	speakers, err := m.vectorDB.GetAllSpeakers(uid, agentID)
	if err != nil {
		logger.Errorf("Failed to get speakers from vector database: %v", err)
		return []*SpeakerInfo{}
	}
	return speakers
}

// DeleteSpeaker xóa loa (hỗ trợ cách ly kích thước UID và Agent ID)
func (m *Manager) DeleteSpeaker(uid, agentID, speakerID string) error {
	if uid == "" {
		return fmt.Errorf("uid is required")
	}

	// Xóa khỏi cơ sở dữ liệu vector
	err := m.vectorDB.DeleteByFilters(uid, agentID, speakerID)
	if err != nil {
		return fmt.Errorf("failed to delete from vector database: %v", err)
	}

	logger.Infof("Successfully deleted speaker %s for uid %s, agent_id %s", speakerID, uid, agentID)
	return nil
}

// DeleteSpeakerByUUID xóa loa bằng UUID (hỗ trợ cách ly kích thước UID và Agent ID)
func (m *Manager) DeleteSpeakerByUUID(uid, agentID, uuid string) error {
	if uid == "" {
		return fmt.Errorf("uid is required")
	}

	if uuid == "" {
		return fmt.Errorf("uuid is required")
	}

	// Xóa khỏi cơ sở dữ liệu vectơ Qdrant
	err := m.vectorDB.DeleteByUUID(uid, agentID, uuid)
	if err != nil {
		return fmt.Errorf("failed to delete from vector database: %v", err)
	}

	logger.Infof("Successfully deleted speaker with uuid %s for uid %s, agent_id %s", uuid, uid, agentID)
	return nil
}

// GetStats lấy thông tin thống kê (dùng để giám sát dịch vụ chính, hỗ trợ lọc theo UID và ID tác nhân)
func (m *Manager) GetStats(uid, agentID string) map[string]interface{} {
	stats := m.GetDatabaseStats(uid, agentID)

	return map[string]interface{}{
		"speaker_count": stats.TotalSpeakers,
		"total_samples": stats.TotalSamples,
		"embedding_dim": stats.EmbeddingDim,
		"threshold":     stats.Threshold,
		"version":       stats.Version,
		"last_updated":  stats.UpdatedAt.Format(time.RFC3339),
	}
}

// GetDatabaseStats Lấy số liệu thống kê cơ sở dữ liệu (hỗ trợ lọc theo UID và ID tác nhân)
func (m *Manager) GetDatabaseStats(uid, agentID string) *DatabaseStats {
	// Nhận số liệu thống kê từ cơ sở dữ liệu vector
	speakers, err := m.vectorDB.GetAllSpeakers(uid, agentID)
	if err != nil {
		logger.Errorf("Failed to get speakers from vector database: %v", err)
		return &DatabaseStats{
			TotalSpeakers: 0,
			TotalSamples:  0,
			EmbeddingDim:  m.embeddingDim,
			Threshold:     m.threshold,
			Version:       "2.0.0",
			UpdatedAt:     time.Now(),
		}
	}

	totalSamples := 0
	for _, speaker := range speakers {
		totalSamples += speaker.SampleCount
	}

	return &DatabaseStats{
		TotalSpeakers: len(speakers),
		TotalSamples:  totalSamples,
		EmbeddingDim:  m.embeddingDim,
		Threshold:     m.threshold,
		Version:       "2.0.0",
		UpdatedAt:     time.Now(),
	}
}

// Định nghĩa cấu trúc phản hồi
type IdentifyResult struct {
	Identified  bool    `json:"identified"`
	SpeakerID   string  `json:"speaker_id"`
	SpeakerName string  `json:"speaker_name"`
	Confidence  float32 `json:"confidence"`
	Threshold   float32 `json:"threshold"`
}

type VerifyResult struct {
	SpeakerID   string  `json:"speaker_id"`
	SpeakerName string  `json:"speaker_name"`
	Verified    bool    `json:"verified"`
	Confidence  float32 `json:"confidence"`
	Threshold   float32 `json:"threshold"`
}

type SpeakerInfo struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	UUID        string    `json:"uuid"`
	AgentID     string    `json:"agent_id"`
	SampleCount int       `json:"sample_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DatabaseStats struct {
	TotalSpeakers int       `json:"total_speakers"`
	TotalSamples  int       `json:"total_samples"`
	EmbeddingDim  int       `json:"embedding_dim"`
	Threshold     float32   `json:"threshold"`
	Version       string    `json:"version"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// StreamingIdentifier Trình nhận dạng giọng nói trực tuyến
type StreamingIdentifier struct {
	manager     *Manager
	uid         string // ID người dùng, nếu là chuỗi trống, nó sẽ không được sử dụng làm điều kiện lọc.
	agentID     string // ID tác nhân, nếu là chuỗi trống, nó sẽ không được sử dụng làm điều kiện lọc.
	speakerID   string // ID người nói. Nếu là chuỗi rỗng thì nó sẽ không được sử dụng làm điều kiện lọc.
	speakerName string // Tên diễn giả. Nếu là chuỗi rỗng thì nó sẽ không được sử dụng làm điều kiện lọc.
	stream      *sherpa.OnlineStream
	sampleRate  int
	threshold   float32 // Ngưỡng nhận dạng, nếu <= 0 thì sử dụng ngưỡng mặc định
	mutex       sync.Mutex
	isFinished  bool
}

// NewStreamingIdentifier tạo mã định danh phát trực tuyến (hỗ trợ lọc UID, Agent_id, loa_id và loa_name tùy chọn)
// uid: ID người dùng, nếu là chuỗi rỗng thì sẽ không được dùng làm điều kiện lọc
// ID đại lý: ID đại lý. Nếu là chuỗi rỗng thì nó sẽ không được sử dụng làm điều kiện lọc.
// loaID: ID loa, nếu là chuỗi trống thì sẽ không được dùng làm điều kiện lọc
// loaName: tên loa, nếu là chuỗi rỗng thì sẽ không được dùng làm điều kiện lọc
// ngưỡng: ngưỡng nhận dạng, nếu <= 0 thì sử dụng ngưỡng mặc định
func (m *Manager) NewStreamingIdentifier(uid, agentID, speakerID, speakerName string, sampleRate int, threshold ...float32) *StreamingIdentifier {
	stream := m.extractor.CreateStream()
	useThreshold := m.threshold
	if len(threshold) > 0 && threshold[0] > 0 {
		useThreshold = threshold[0]
	}
	return &StreamingIdentifier{
		manager:     m,
		uid:         uid,
		agentID:     agentID,
		speakerID:   speakerID,
		speakerName: speakerName,
		stream:      stream,
		sampleRate:  sampleRate,
		threshold:   useThreshold,
		isFinished:  false,
	}
}

// AcceptAudio nhận các khối dữ liệu âm thanh (đầu vào phát trực tuyến)
func (si *StreamingIdentifier) AcceptAudio(audioData []float32) error {
	si.mutex.Lock()
	defer si.mutex.Unlock()

	if si.isFinished {
		return fmt.Errorf("stream already finished")
	}

	if si.stream == nil {
		return fmt.Errorf("stream is nil")
	}

	// Chấp nhận khối dữ liệu âm thanh
	si.stream.AcceptWaveform(si.sampleRate, audioData)
	return nil
}

// FinishAndIdentify hoàn thành việc nhập và xác định giọng nói
func (si *StreamingIdentifier) FinishAndIdentify() (*IdentifyResult, error) {
	si.mutex.Lock()
	defer si.mutex.Unlock()

	if si.isFinished {
		return nil, fmt.Errorf("stream already finished")
	}

	if si.stream == nil {
		return nil, fmt.Errorf("stream is nil")
	}

	// Đánh dấu đầu vào đã hoàn thành
	si.stream.InputFinished()
	si.isFinished = true

	// Kiểm tra xem nó đã sẵn sàng chưa
	if !si.manager.extractor.IsReady(si.stream) {
		si.cleanup()
		return nil, fmt.Errorf("insufficient audio data for embedding extraction")
	}

	// Trích xuất tính năng
	embedding := si.manager.extractor.Compute(si.stream)
	if len(embedding) == 0 {
		si.cleanup()
		return nil, fmt.Errorf("failed to extract embedding")
	}

	// Xác định ngưỡng nào sẽ sử dụng: nếu ngưỡng tùy chỉnh được đặt, hãy sử dụng ngưỡng đó, nếu không hãy sử dụng ngưỡng mặc định
	useThreshold := si.manager.threshold
	if si.threshold > 0 {
		useThreshold = si.threshold
	}

	// Tìm kiếm trong cơ sở dữ liệu vectơ Qdrant (được lọc theo UID tùy chọn, Agent_id, loa_id và loa_name, trả về top 1)
	results, err := si.manager.vectorDB.SearchWithOptionalFilters(si.uid, si.agentID, si.speakerID, si.speakerName, embedding, useThreshold, 1)
	if err != nil {
		si.cleanup()
		return nil, fmt.Errorf("failed to search in vector database: %v", err)
	}

	//Ghi lại kết quả
	logger.Debugf("Search results: %+v", results)

	result := &IdentifyResult{
		Identified:  false,
		SpeakerID:   "",
		SpeakerName: "",
		Confidence:  0.0,
		Threshold:   useThreshold,
	}

	if len(results) > 0 {
		bestMatch := results[0]
		result.Identified = true
		result.SpeakerID = bestMatch.SpeakerID
		result.SpeakerName = bestMatch.SpeakerName
		result.Confidence = bestMatch.Confidence
	}

	// Dọn dẹp tài nguyên
	si.cleanup()

	return result, nil
}

// dọn dẹp dọn dẹp tài nguyên
func (si *StreamingIdentifier) cleanup() {
	if si.stream != nil {
		sherpa.DeleteOnlineStream(si.stream)
		si.stream = nil
	}
}

// Đóng đóng trình nhận dạng phát trực tuyến và giải phóng tài nguyên
func (si *StreamingIdentifier) Close() {
	si.mutex.Lock()
	defer si.mutex.Unlock()
	si.cleanup()
}
