package speaker

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"voice_server/internal/logger"
)

// Triển khai lưu trữ tệp JSONVectorDB JSON
// Sử dụng các tệp JSON cục bộ để lưu trữ dữ liệu vectơ giọng nói, phù hợp cho các hoạt động triển khai nhỏ
type JSONVectorDB struct {
	filePath     string
	data         *SpeakerData
	mutex        sync.RWMutex
	embeddingDim int
}

// Cấu hình lưu trữ JSONVectorDBConfig JSON
type JSONVectorDBConfig struct {
	FilePath     string // Đường dẫn tệp JSON
	EmbeddingDim int    // kích thước vector
}

// Cấu trúc dữ liệu JSON của loaData
type SpeakerData struct {
	Version   int64                    `json:"version"`
	UpdatedAt int64                    `json:"updated_at"`
	Speakers  map[string]*SpeakerEntry `json:"speakers"`
}

// LoaMục nhập loa
type SpeakerEntry struct {
	UID         string       `json:"uid"`
	AgentID     string       `json:"agent_id"`
	SpeakerID   string       `json:"speaker_id"`
	SpeakerName string       `json:"speaker_name"`
	CreatedAt   int64        `json:"created_at"`
	UpdatedAt   int64        `json:"updated_at"`
	Embeddings  []*Embedding `json:"embeddings"`
}

// Nhúng mục nhập vector
type Embedding struct {
	UUID        string    `json:"uuid"`
	SampleIndex int       `json:"sample_index"`
	Vector      []float32 `json:"vector"`
	CreatedAt   int64     `json:"created_at"`
}

// NewJSONVectorDB tạo cơ sở dữ liệu vectơ JSON
func NewJSONVectorDB(config *JSONVectorDBConfig) (*JSONVectorDB, error) {
	// Đảm bảo thư mục tồn tại
	dir := filepath.Dir(config.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %v", err)
	}

	db := &JSONVectorDB{
		filePath:     config.FilePath,
		embeddingDim: config.EmbeddingDim,
		data: &SpeakerData{
			Version:  1,
			Speakers: make(map[string]*SpeakerEntry),
		},
	}

	// Tải dữ liệu hiện có
	if err := db.load(); err != nil {
		// Nếu tệp không tồn tại, hãy tạo dữ liệu trống
		if os.IsNotExist(err) {
			logger.Infof("JSON vector DB file not found, creating new one: %s", config.FilePath)
			if err := db.save(); err != nil {
				return nil, fmt.Errorf("failed to create new DB file: %v", err)
			}
		} else {
			return nil, fmt.Errorf("failed to load JSON DB: %v", err)
		}
	}

	return db, nil
}

// Init khởi tạo cơ sở dữ liệu (thực hiện giao diện)
func (db *JSONVectorDB) Init() error {
	return nil // Được khởi tạo trong hàm tạo
}

// Đóng đóng cơ sở dữ liệu (thực hiện giao diện)
func (db *JSONVectorDB) Close() error {
	// lưu dữ liệu
	return db.save()
}

// tải tải dữ liệu từ tập tin
func (db *JSONVectorDB) load() error {
	db.mutex.Lock()
	defer db.mutex.Unlock()

	data, err := os.ReadFile(db.filePath)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return fmt.Errorf("empty file")
	}

	if err := json.Unmarshal(data, db.data); err != nil {
		return err
	}

	logger.Infof("Loaded JSON vector DB: %d speakers from %s", len(db.data.Speakers), db.filePath)
	return nil
}

// lưu lưu dữ liệu vào một tệp (sẽ bị khóa để sử dụng bởi những người gọi không giữ khóa, chẳng hạn như Đóng, NewJSONVectorDB)
func (db *JSONVectorDB) save() error {
	db.mutex.Lock()
	defer db.mutex.Unlock()
	return db.saveUnlocked()
}

// saveUnlocked chỉ ghi logic đĩa mà không khóa; người gọi phải giữ khóa ghi db.mutex để tránh bế tắc
func (db *JSONVectorDB) saveUnlocked() error {
	db.data.UpdatedAt = time.Now().Unix()

	data, err := json.MarshalIndent(db.data, "", "  ")
	if err != nil {
		return err
	}

	// Ghi nguyên tử: đầu tiên ghi vào tệp tạm thời, sau đó đổi tên
	tempPath := db.filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return err
	}

	if err := os.Rename(tempPath, db.filePath); err != nil {
		return err
	}

	return nil
}

// generateKey tạo một khóa duy nhất
func generateKey(uid, agentID, speakerID string) string {
	return fmt.Sprintf("%s:%s:%s", uid, agentID, speakerID)
}

// Chèn chèn vectơ giọng nói (triển khai giao diện); nếu uid+agentID+uuid đã tồn tại, hãy cập nhật mục (cùng một uuid được coi là cùng một giọng nói)
func (db *JSONVectorDB) Insert(uid, agentID, speakerID, speakerName, uuid string, embedding []float32, sampleIndex int, createdAt, updatedAt int64) error {
	db.mutex.Lock()
	defer db.mutex.Unlock()

	key := generateKey(uid, agentID, speakerID)

	entry, exists := db.data.Speakers[key]
	if !exists {
		entry = &SpeakerEntry{
			UID:         uid,
			AgentID:     agentID,
			SpeakerID:   speakerID,
			SpeakerName: speakerName,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			Embeddings:  make([]*Embedding, 0),
		}
		db.data.Speakers[key] = entry
	}

	// Tìm kiếm đầu tiên theo uuid: nếu uuid giống nhau, hãy cập nhật mục nhập
	for i, emb := range entry.Embeddings {
		if emb.UUID == uuid {
			entry.Embeddings[i] = &Embedding{
				UUID:        uuid,
				SampleIndex: emb.SampleIndex, // Giữ sample_index ban đầu
				Vector:      embedding,
				CreatedAt:   createdAt,
			}
			entry.UpdatedAt = updatedAt
			return db.saveToDiskAsync()
		}
	}

	// Kiểm tra xem phần nhúng có cùng sample_index đã tồn tại chưa (ghi đè dữ liệu cũ)
	for i, emb := range entry.Embeddings {
		if emb.SampleIndex == sampleIndex {
			entry.Embeddings[i] = &Embedding{
				UUID:        uuid,
				SampleIndex: sampleIndex,
				Vector:      embedding,
				CreatedAt:   createdAt,
			}
			entry.UpdatedAt = updatedAt
			return db.saveToDiskAsync()
		}
	}

	// Thêm nhúng mới
	entry.Embeddings = append(entry.Embeddings, &Embedding{
		UUID:        uuid,
		SampleIndex: sampleIndex,
		Vector:      embedding,
		CreatedAt:   createdAt,
	})
	entry.UpdatedAt = updatedAt

	return db.saveToDiskAsync()
}

// saveToDiskAsync được sử dụng để lưu đĩa khi khóa ghi đã được giữ. Chỉ saveUnlocked được gọi nội bộ để tránh tình trạng khóa bế tắc lặp đi lặp lại.
func (db *JSONVectorDB) saveToDiskAsync() error {
	return db.saveUnlocked()
}

// Tìm kiếm tìm kiếm các vectơ tương tự (thực hiện giao diện)
func (db *JSONVectorDB) Search(uid string, queryEmbedding []float32, threshold float32, topK int) ([]SearchResult, error) {
	return db.SearchWithOptionalFilters(uid, "", "", "", queryEmbedding, threshold, topK)
}

// SearchWithOptionalFilters tìm kiếm các vectơ tương tự (triển khai giao diện)
func (db *JSONVectorDB) SearchWithOptionalFilters(uid, agentID, speakerID, speakerName string, queryEmbedding []float32, threshold float32, topK int) ([]SearchResult, error) {
	db.mutex.RLock()
	defer db.mutex.RUnlock()

	var results []SearchResult

	for _, speaker := range db.data.Speakers {
		// Áp dụng bộ lọc
		if uid != "" && speaker.UID != uid {
			continue
		}
		if agentID != "" && speaker.AgentID != agentID {
			continue
		}
		if speakerID != "" && speaker.SpeakerID != speakerID {
			continue
		}
		if speakerName != "" && speaker.SpeakerName != speakerName {
			continue
		}

		// Tính toán độ giống nhau của mỗi lần nhúng
		for _, emb := range speaker.Embeddings {
			similarity := cosineSimilarity(queryEmbedding, emb.Vector)
			if similarity >= threshold {
				results = append(results, SearchResult{
					SpeakerID:   speaker.SpeakerID,
					SpeakerName: speaker.SpeakerName,
					Confidence:  similarity,
					Distance:    1.0 - similarity,
					SampleIndex: emb.SampleIndex,
				})
			}
		}
	}

	// Sắp xếp theo độ tin cậy
	sort.Slice(results, func(i, j int) bool {
		return results[i].Confidence > results[j].Confidence
	})

	// Trở về đầu trangK
	if len(results) > topK && topK > 0 {
		results = results[:topK]
	}

	return results, nil
}

// SearchWithFilter tìm kiếm các vectơ tương tự (triển khai giao diện)
func (db *JSONVectorDB) SearchWithFilter(uid, agentID, speakerID string, queryEmbedding []float32, threshold float32, topK int) ([]SearchResult, error) {
	return db.SearchWithOptionalFilters(uid, agentID, speakerID, "", queryEmbedding, threshold, topK)
}

// GetSpeakerSampleCount Lấy số lượng mẫu của người nói (triển khai giao diện)
func (db *JSONVectorDB) GetSpeakerSampleCount(uid, agentID, speakerID string) (int, error) {
	db.mutex.RLock()
	defer db.mutex.RUnlock()

	key := generateKey(uid, agentID, speakerID)
	if entry, exists := db.data.Speakers[key]; exists {
		return len(entry.Embeddings), nil
	}
	return 0, nil
}

// GetSpeakerInfo lấy thông tin người nói (triển khai giao diện)
func (db *JSONVectorDB) GetSpeakerInfo(uid, agentID, speakerID string) (*SpeakerInfo, error) {
	db.mutex.RLock()
	defer db.mutex.RUnlock()

	key := generateKey(uid, agentID, speakerID)
	entry, exists := db.data.Speakers[key]
	if !exists {
		return nil, fmt.Errorf("speaker %s not found", speakerID)
	}

	return &SpeakerInfo{
		ID:          entry.SpeakerID,
		Name:        entry.SpeakerName,
		SampleCount: len(entry.Embeddings),
		CreatedAt:   time.Unix(entry.CreatedAt, 0),
		UpdatedAt:   time.Unix(entry.UpdatedAt, 0),
	}, nil
}

// GetAllSpeakers Lấy danh sách tất cả các diễn giả (triển khai giao diện)
func (db *JSONVectorDB) GetAllSpeakers(uid, agentID string) ([]*SpeakerInfo, error) {
	db.mutex.RLock()
	defer db.mutex.RUnlock()

	var speakers []*SpeakerInfo

	for _, entry := range db.data.Speakers {
		// Áp dụng bộ lọc
		if uid != "" && entry.UID != uid {
			continue
		}
		if agentID != "" && entry.AgentID != agentID {
			continue
		}

		speakers = append(speakers, &SpeakerInfo{
			ID:          entry.SpeakerID,
			Name:        entry.SpeakerName,
			AgentID:     entry.AgentID,
			SampleCount: len(entry.Embeddings),
			CreatedAt:   time.Unix(entry.CreatedAt, 0),
			UpdatedAt:   time.Unix(entry.UpdatedAt, 0),
		})
	}

	return speakers, nil
}

// DeleteByFilters xóa loa (triển khai giao diện)
func (db *JSONVectorDB) DeleteByFilters(uid, agentID, speakerID string) error {
	db.mutex.Lock()
	defer db.mutex.Unlock()

	key := generateKey(uid, agentID, speakerID)
	if _, exists := db.data.Speakers[key]; !exists {
		return nil // Ngay cả khi nó không tồn tại, nó vẫn được coi là thành công.
	}

	delete(db.data.Speakers, key)
	return db.saveUnlocked()
}

// DeleteByUUID xóa loa bằng UUID (thực hiện giao diện)
func (db *JSONVectorDB) DeleteByUUID(uid, agentID, uuid string) error {
	db.mutex.Lock()
	defer db.mutex.Unlock()

	// Tìm loa chứa UUID này
	for key, entry := range db.data.Speakers {
		if entry.UID != uid || (agentID != "" && entry.AgentID != agentID) {
			continue
		}

		// Tìm và xóa các nội dung nhúng phù hợp
		for i, emb := range entry.Embeddings {
			if emb.UUID == uuid {
				// Xóa phần nhúng
				entry.Embeddings = append(entry.Embeddings[:i], entry.Embeddings[i+1:]...)

				// Nếu không có nhúng thì xóa toàn bộ loa
				if len(entry.Embeddings) == 0 {
					delete(db.data.Speakers, key)
				}

				return db.saveUnlocked()
			}
		}
	}

	return fmt.Errorf("speaker with uuid %s not found for uid %s", uuid, uid)
}

// cosineSimilarity tính toán độ tương tự cosine
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct float32
	var normA float32
	var normB float32

	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}
