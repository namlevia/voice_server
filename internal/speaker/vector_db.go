package speaker

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"time"

	"voice_server/internal/logger"

	"github.com/qdrant/go-client/qdrant"
)

// Giao diện cơ sở dữ liệu vector VectorDatabase
// Xác định giao diện hoạt động hợp nhất cho cơ sở dữ liệu vectơ và hỗ trợ nhiều phụ trợ lưu trữ (Qdrant, JSON, v.v.)
type VectorDatabase interface {
	// Init khởi tạo cơ sở dữ liệu
	Init() error

	// Đóng đóng kết nối cơ sở dữ liệu
	Close() error

	// Chèn chèn vector dấu giọng nói
	Insert(uid, agentID, speakerID, speakerName, uuid string, embedding []float32, sampleIndex int, createdAt, updatedAt int64) error

	// Tìm kiếm Tìm kiếm các vectơ tương tự (chỉ lọc theo UID)
	Search(uid string, queryEmbedding []float32, threshold float32, topK int) ([]SearchResult, error)

	// SearchWithOptionalFilters tìm kiếm các vectơ tương tự (hỗ trợ lọc UID, Agent_id, loa_id và loa_name tùy chọn)
	SearchWithOptionalFilters(uid, agentID, speakerID, speakerName string, queryEmbedding []float32, threshold float32, topK int) ([]SearchResult, error)

	// SearchWithFilter tìm kiếm các vectơ tương tự (được lọc nghiêm ngặt theo UID, Agent_id và loa_id)
	SearchWithFilter(uid, agentID, speakerID string, queryEmbedding []float32, threshold float32, topK int) ([]SearchResult, error)

	// GetSpeakerSampleCount Lấy số lượng mẫu của người nói
	GetSpeakerSampleCount(uid, agentID, speakerID string) (int, error)

	// GetSpeakerInfo Nhận thông tin người nói
	GetSpeakerInfo(uid, agentID, speakerID string) (*SpeakerInfo, error)

	// GetAllSpeakers Lấy danh sách tất cả các diễn giả
	GetAllSpeakers(uid, agentID string) ([]*SpeakerInfo, error)

	// DeleteByFilters xóa loa (thông qua điều kiện lọc)
	DeleteByFilters(uid, agentID, speakerID string) error

	// DeleteByUUID Xóa loa bằng UUID
	DeleteByUUID(uid, agentID, uuid string) error
}

// Cấu hình QdrantConfig Qdrant
type QdrantConfig struct {
	Host           string
	Port           int
	CollectionName string
}

// Máy khách cơ sở dữ liệu vectơ QdrantVectorDB Qdrant
type QdrantVectorDB struct {
	client         *qdrant.Client
	collectionName string
	embeddingDim   int
}

// Kết quả tìm kiếm Kết quả tìm kiếm
type SearchResult struct {
	SpeakerID   string
	SpeakerName string
	Confidence  float32
	Distance    float32
	SampleIndex int
}

// NewQdrantVectorDB tạo máy khách cơ sở dữ liệu vector Qdrant
func NewQdrantVectorDB(config *QdrantConfig, embeddingDim int) (*QdrantVectorDB, error) {
	// Kết nối Qdrant
	client, err := qdrant.NewClient(&qdrant.Config{
		Host: config.Host,
		Port: config.Port,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Qdrant: %v", err)
	}

	db := &QdrantVectorDB{
		client:         client,
		collectionName: config.CollectionName,
		embeddingDim:   embeddingDim,
	}

	return db, nil
}

// Ban đầu khởi tạo cơ sở dữ liệu vectơ Qdrant (đảm bảo Bộ sưu tập tồn tại)
// Triển khai giao diện VectorDatabase
func (db *QdrantVectorDB) Init() error {
	ctx := context.Background()
	return db.ensureCollectionExists(ctx)
}

// normalizeVector L2 chuẩn hóa một vectơ
// Công thức: v_normalized = v / ||v||
// Khi vectơ được chuẩn hóa, tích số chấm = độ tương tự cosine
func normalizeVector(v []float32) []float32 {
	// Tính định mức L2
	var norm float32
	for _, val := range v {
		norm += val * val
	}
	norm = float32(math.Sqrt(float64(norm)))

	// bình thường hóa
	if norm == 0 {
		return v // Vector 0 được trả về trực tiếp
	}

	normalized := make([]float32, len(v))
	for i := range v {
		normalized[i] = v[i] / norm
	}
	return normalized
}

// generatePointID tạo ID điểm duy nhất
func generatePointID(uid, agentID, speakerID string, sampleIndex int) uint64 {
	hash := fnv.New64a()
	hash.Write([]byte(fmt.Sprintf("%s:%s:%s:%d", uid, agentID, speakerID, sampleIndex)))
	return hash.Sum64()
}

// đảmCollectionExists đảm bảo rằng Bộ sưu tập tồn tại, tạo nó nếu nó không tồn tại
func (db *QdrantVectorDB) ensureCollectionExists(ctx context.Context) error {
	_, err := db.client.GetCollectionInfo(ctx, db.collectionName)
	if err != nil {
		// Bộ sưu tập không tồn tại, hãy tạo nó
		logger.Infof("Collection '%s' does not exist, creating it...", db.collectionName)
		err = db.client.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: db.collectionName,
			VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
				Size:     uint64(db.embeddingDim),
				Distance: qdrant.Distance_Cosine, // Sử dụng khoảng cách cosine (chuẩn hóa tự động Qdrant)
			}),
		})
		if err != nil {
			return fmt.Errorf("failed to create collection: %v", err)
		}
		logger.Infof("✅ Collection '%s' created successfully", db.collectionName)
	}
	return nil
}

// Chèn phần chèn nhúng vào cơ sở dữ liệu vectơ; nếu uid+agentID+uuid đã tồn tại, hãy cập nhật mục nhập (cùng một uuid được coi là cùng một giọng nói)
func (db *QdrantVectorDB) Insert(uid, agentID, speakerID, speakerName, uuid string, embedding []float32, sampleIndex int, createdAt, updatedAt int64) error {
	ctx := context.Background()

	// Đảm bảo Bộ sưu tập tồn tại (tạo nó nếu nó không tồn tại)
	if err := db.ensureCollectionExists(ctx); err != nil {
		return fmt.Errorf("failed to ensure collection exists: %v", err)
	}

	// Đầu tiên nhấn uuid để kiểm tra xem giọng nói đã tồn tại chưa (uid+agentID+uuid là duy nhất)
	conditions := []*qdrant.Condition{
		qdrant.NewMatch("uid", uid),
		qdrant.NewMatch("uuid", uuid),
	}
	if agentID != "" {
		conditions = append(conditions, qdrant.NewMatch("agent_id", agentID))
	}
	filter := &qdrant.Filter{Must: conditions}
	limit := uint32(1)
	scrollResult, err := db.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: db.collectionName,
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return fmt.Errorf("failed to scroll by uuid: %v", err)
	}

	var point *qdrant.PointStruct
	if len(scrollResult) > 0 {
		// uuid đã tồn tại: sử dụng Id của Điểm ban đầu và sample_index để thực hiện Upsert (cập nhật vector và update_at, giữ lại create_at)
		existing := scrollResult[0]
		payload := existing.GetPayload()
		useSampleIndex := sampleIndex
		useCreatedAt := createdAt
		if val, ok := payload["sample_index"]; ok {
			useSampleIndex = int(val.GetIntegerValue())
		}
		if val, ok := payload["created_at"]; ok {
			useCreatedAt = val.GetIntegerValue()
		}
		point = &qdrant.PointStruct{
			Id:      existing.Id,
			Vectors: qdrant.NewVectors(embedding...),
			Payload: qdrant.NewValueMap(map[string]any{
				"uid":          uid,
				"agent_id":     agentID,
				"speaker_id":   speakerID,
				"speaker_name": speakerName,
				"uuid":         uuid,
				"sample_index": useSampleIndex,
				"created_at":   useCreatedAt,
				"updated_at":   updatedAt,
			}),
		}
	} else {
		// uuid không tồn tại: tạo PointId mới bằng cách chèn sample_index
		pointID := generatePointID(uid, agentID, speakerID, sampleIndex)
		point = &qdrant.PointStruct{
			Id:      qdrant.NewIDNum(pointID),
			Vectors: qdrant.NewVectors(embedding...),
			Payload: qdrant.NewValueMap(map[string]any{
				"uid":          uid,
				"agent_id":     agentID,
				"speaker_id":   speakerID,
				"speaker_name": speakerName,
				"uuid":         uuid,
				"sample_index": sampleIndex,
				"created_at":   createdAt,
				"updated_at":   updatedAt,
			}),
		}
	}

	_, err = db.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: db.collectionName,
		Points:         []*qdrant.PointStruct{point},
	})
	if err != nil {
		return fmt.Errorf("failed to insert point: %v", err)
	}

	return nil
}

// Tìm kiếm Tìm kiếm các vectơ tương tự (được lọc theo UID)
func (db *QdrantVectorDB) Search(uid string, queryEmbedding []float32, threshold float32, topK int) ([]SearchResult, error) {
	return db.SearchWithOptionalFilters(uid, "", "", "", queryEmbedding, threshold, topK)
}

// SearchWithOptionalFilters tìm kiếm các vectơ tương tự (hỗ trợ lọc UID, Agent_id, loa_id và loa_name tùy chọn)
// uid: ID người dùng, nếu là chuỗi rỗng thì sẽ không được dùng làm điều kiện lọc
// ID đại lý: ID đại lý. Nếu là chuỗi rỗng thì nó sẽ không được sử dụng làm điều kiện lọc.
// loaID: ID loa, nếu là chuỗi trống thì sẽ không được dùng làm điều kiện lọc
// loaName: tên loa, nếu là chuỗi rỗng thì sẽ không được dùng làm điều kiện lọc
func (db *QdrantVectorDB) SearchWithOptionalFilters(uid, agentID, speakerID, speakerName string, queryEmbedding []float32, threshold float32, topK int) ([]SearchResult, error) {
	ctx := context.Background()

	// Xây dựng điều kiện lọc (lọc theo UID, Agent_id, loa_id và loa_name, nếu trống thì không thêm điều kiện)
	conditions := make([]*qdrant.Condition, 0)
	if uid != "" {
		conditions = append(conditions, qdrant.NewMatch("uid", uid))
	}
	if agentID != "" {
		conditions = append(conditions, qdrant.NewMatch("agent_id", agentID))
	}
	if speakerID != "" {
		conditions = append(conditions, qdrant.NewMatch("speaker_id", speakerID))
	}
	if speakerName != "" {
		conditions = append(conditions, qdrant.NewMatch("speaker_name", speakerName))
	}

	var filter *qdrant.Filter
	if len(conditions) > 0 {
		filter = &qdrant.Filter{
			Must: conditions,
		}
	}

	limit := uint64(topK)
	if limit == 0 {
		limit = 1
	}

	// L2 chuẩn hóa truy vấnNhúng (Khoảng cách DOT yêu cầu chuẩn hóa vectơ)
	normalizedQueryEmbedding := normalizeVector(queryEmbedding)

	// Tìm kiếm bằng API truy vấn
	queryPoints := &qdrant.QueryPoints{
		CollectionName: db.collectionName,
		Query:          qdrant.NewQuery(normalizedQueryEmbedding...),
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	}
	if filter != nil {
		queryPoints.Filter = filter
	}

	// In thông tin queryPoints
	logger.Debugf("QueryPoints: CollectionName=%s, Limit=%d, WithPayload=%v, QueryEmbeddingLen=%d",
		queryPoints.CollectionName, *queryPoints.Limit, queryPoints.WithPayload, len(normalizedQueryEmbedding))
	if filter != nil {
		logger.Debugf("  Filter: HasFilter=true, MustConditionsCount=%d", len(filter.Must))
		for i, condition := range filter.Must {
			logger.Debugf("    Filter.Must[%d]: %+v", i, condition)
		}
	} else {
		logger.Debugf("  Filter: HasFilter=false")
	}

	searchPoints, err := db.client.Query(ctx, queryPoints)
	if err != nil {
		return nil, fmt.Errorf("failed to search: %v", err)
	}

	// Kết quả chuyển đổi
	results := make([]SearchResult, 0)
	for _, point := range searchPoints {
		if point.Payload == nil {
			continue
		}

		payload := point.GetPayload()
		var speakerID string
		var speakerName string
		var sampleIndex int

		if val, ok := payload["speaker_id"]; ok {
			speakerID = val.GetStringValue()
		}
		if val, ok := payload["speaker_name"]; ok {
			speakerName = val.GetStringValue()
		}
		if val, ok := payload["sample_index"]; ok {
			sampleIndex = int(val.GetIntegerValue())
		}

		// Điểm được API truy vấn trả về là độ tương tự cosine (phạm vi [-1, 1])
		// Khi sử dụng Distance_Cosine, Qdrant sẽ tự động chuẩn hóa các vectơ và tính toán độ tương tự cosine
		score := float32(point.Score)

		// Quan trọng: cosineSimilarity() của trình quản lý trả về trực tiếp độ tương tự cosine (phạm vi [-1, 1])
		// Để nhất quán với Trình quản lý, Qdrant cũng nên sử dụng điểm trực tiếp mà không cần chuyển đổi.
		var confidence float32
		if score < -1 {
			confidence = -1.0
		} else if score > 1 {
			confidence = 1.0
		} else {
			// Sử dụng điểm trực tiếp (phạm vi [-1, 1]), phù hợp với độ tương tự cosine của Người quản lý
			confidence = score
		}

		// Áp dụng lọc ngưỡng
		if confidence < threshold {
			continue
		}

		distance := 1.0 - confidence

		results = append(results, SearchResult{
			SpeakerID:   speakerID,
			SpeakerName: speakerName,
			Confidence:  confidence,
			Distance:    distance,
			SampleIndex: sampleIndex,
		})
	}

	return results, nil
}

// SearchWithFilter tìm kiếm các vectơ tương tự (được lọc theo UID, Agent_id và loa_id)
func (db *QdrantVectorDB) SearchWithFilter(uid, agentID, speakerID string, queryEmbedding []float32, threshold float32, topK int) ([]SearchResult, error) {
	ctx := context.Background()

	// Xây dựng bộ lọc (lọc theo UID, Agent_id và loa_id)
	conditions := []*qdrant.Condition{
		qdrant.NewMatch("uid", uid),
		qdrant.NewMatch("speaker_id", speakerID),
	}
	// Nếu ID tác nhân không trống, hãy thêm vào điều kiện lọc
	if agentID != "" {
		conditions = append(conditions, qdrant.NewMatch("agent_id", agentID))
	}
	filter := &qdrant.Filter{
		Must: conditions,
	}

	limit := uint64(topK)
	if limit == 0 {
		limit = 1
	}

	// Lưu ý: Qdrant sẽ tự động chuẩn hóa vectơ truy vấn khi sử dụng Distance_Cosine
	// Do đó, không cần phải chuẩn hóa thủ công trong chương trình (ngay cả khi vectơ đến đã được chuẩn hóa, Qdrant có thể chuẩn hóa lại mà không gặp vấn đề gì)

	// Tìm kiếm bằng API truy vấn
	searchPoints, err := db.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: db.collectionName,
		Query:          qdrant.NewQuery(queryEmbedding...),
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to search: %v", err)
	}

	// Kết quả chuyển đổi (giống như phương pháp Tìm kiếm)
	results := make([]SearchResult, 0)
	for _, point := range searchPoints {
		if point.Payload == nil {
			continue
		}

		payload := point.GetPayload()
		var foundSpeakerID string
		var speakerName string
		var sampleIndex int

		if val, ok := payload["speaker_id"]; ok {
			foundSpeakerID = val.GetStringValue()
		}
		if val, ok := payload["speaker_name"]; ok {
			speakerName = val.GetStringValue()
		}
		if val, ok := payload["sample_index"]; ok {
			sampleIndex = int(val.GetIntegerValue())
		}

		// Điểm được API truy vấn trả về là độ tương tự cosine (phạm vi [-1, 1])
		// Khi sử dụng Distance_Cosine, Qdrant sẽ tự động chuẩn hóa các vectơ và tính toán độ tương tự cosine
		score := float32(point.Score)
		// Quan trọng: cosineSimilarity() của trình quản lý trả về trực tiếp độ tương tự cosine (phạm vi [-1, 1])
		// Để nhất quán với Trình quản lý, Qdrant cũng nên sử dụng điểm trực tiếp mà không cần chuyển đổi.
		var confidence float32
		if score < -1 {
			confidence = -1.0
		} else if score > 1 {
			confidence = 1.0
		} else {
			// Sử dụng điểm trực tiếp (phạm vi [-1, 1]), phù hợp với độ tương tự cosine của Người quản lý
			confidence = score
		}

		if confidence < threshold {
			continue
		}

		distance := 1.0 - confidence

		results = append(results, SearchResult{
			SpeakerID:   foundSpeakerID,
			SpeakerName: speakerName,
			Confidence:  confidence,
			Distance:    distance,
			SampleIndex: sampleIndex,
		})
	}

	return results, nil
}

// GetSpeakerSampleCount Lấy số lượng mẫu của người nói
func (db *QdrantVectorDB) GetSpeakerSampleCount(uid, agentID, speakerID string) (int, error) {
	ctx := context.Background()

	// Nhận tất cả các điểm phù hợp bằng API cuộn
	conditions := []*qdrant.Condition{
		qdrant.NewMatch("uid", uid),
		qdrant.NewMatch("speaker_id", speakerID),
	}
	// Nếu ID tác nhân không trống, hãy thêm vào điều kiện lọc
	if agentID != "" {
		conditions = append(conditions, qdrant.NewMatch("agent_id", agentID))
	}
	filter := &qdrant.Filter{
		Must: conditions,
	}

	limit := uint32(10000) // giá trị đủ lớn
	scrollResult, err := db.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: db.collectionName,
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to scroll points: %v", err)
	}

	return len(scrollResult), nil
}

// GetSpeakerInfo Nhận thông tin người nói
func (db *QdrantVectorDB) GetSpeakerInfo(uid, agentID, speakerID string) (*SpeakerInfo, error) {
	ctx := context.Background()

	// Nhận tất cả các điểm phù hợp bằng API cuộn
	conditions := []*qdrant.Condition{
		qdrant.NewMatch("uid", uid),
		qdrant.NewMatch("speaker_id", speakerID),
	}
	// Nếu ID tác nhân không trống, hãy thêm vào điều kiện lọc
	if agentID != "" {
		conditions = append(conditions, qdrant.NewMatch("agent_id", agentID))
	}
	filter := &qdrant.Filter{
		Must: conditions,
	}

	limit := uint32(10000)
	scrollResult, err := db.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: db.collectionName,
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scroll points: %v", err)
	}

	if len(scrollResult) == 0 {
		return nil, fmt.Errorf("speaker %s not found", speakerID)
	}

	// Trích xuất thông tin từ điểm đầu tiên
	firstPoint := scrollResult[0]
	payload := firstPoint.GetPayload()

	var speakerName string
	var minCreatedAt, maxUpdatedAt int64 = -1, -1

	if val, ok := payload["speaker_name"]; ok {
		speakerName = val.GetStringValue()
	}

	// Duyệt qua tất cả các điểm và tìm ra_at được tạo sớm nhất và được cập nhật_at mới nhất
	for _, point := range scrollResult {
		payload := point.GetPayload()
		if val, ok := payload["created_at"]; ok {
			ts := val.GetIntegerValue()
			if minCreatedAt == -1 || ts < minCreatedAt {
				minCreatedAt = ts
			}
		}
		if val, ok := payload["updated_at"]; ok {
			ts := val.GetIntegerValue()
			if ts > maxUpdatedAt {
				maxUpdatedAt = ts
			}
		}
	}

	if minCreatedAt == -1 {
		minCreatedAt = time.Now().Unix()
	}
	if maxUpdatedAt == -1 {
		maxUpdatedAt = time.Now().Unix()
	}

	return &SpeakerInfo{
		ID:          speakerID,
		Name:        speakerName,
		SampleCount: len(scrollResult),
		CreatedAt:   time.Unix(minCreatedAt, 0),
		UpdatedAt:   time.Unix(maxUpdatedAt, 0),
	}, nil
}

// GetAllSpeakers Nhận danh sách tất cả các diễn giả có UID và ID tác nhân được chỉ định
func (db *QdrantVectorDB) GetAllSpeakers(uid, agentID string) ([]*SpeakerInfo, error) {
	ctx := context.Background()

	// Nhận tất cả các điểm phù hợp bằng API cuộn
	conditions := []*qdrant.Condition{
		qdrant.NewMatch("uid", uid),
	}
	// Nếu ID tác nhân không trống, hãy thêm vào điều kiện lọc
	if agentID != "" {
		conditions = append(conditions, qdrant.NewMatch("agent_id", agentID))
	}
	filter := &qdrant.Filter{
		Must: conditions,
	}

	limit := uint32(10000)
	scrollResult, err := db.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: db.collectionName,
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scroll points: %v", err)
	}

	// Tổng hợp theo loa_id (lưu ý: theo thiết kế mới, mỗi mẫu sử dụng một loa_id khác nhau, vì vậy thực tế chỉ có một mẫu cho mỗi loa_id ở đây)
	speakerMap := make(map[string]*SpeakerInfo)
	for _, point := range scrollResult {
		payload := point.GetPayload()
		var speakerID string
		var speakerName string
		var uuid string
		var agentID string
		var createdAt, updatedAt int64

		if val, ok := payload["speaker_id"]; ok {
			speakerID = val.GetStringValue()
		}
		if val, ok := payload["speaker_name"]; ok {
			speakerName = val.GetStringValue()
		}
		if val, ok := payload["uuid"]; ok {
			uuid = val.GetStringValue()
		}
		if val, ok := payload["agent_id"]; ok {
			agentID = val.GetStringValue()
		}
		if val, ok := payload["created_at"]; ok {
			createdAt = val.GetIntegerValue()
		}
		if val, ok := payload["updated_at"]; ok {
			updatedAt = val.GetIntegerValue()
		}

		if speakerID == "" {
			continue
		}

		info, exists := speakerMap[speakerID]
		if !exists {
			info = &SpeakerInfo{
				ID:          speakerID,
				Name:        speakerName,
				UUID:        uuid,
				AgentID:     agentID,
				SampleCount: 0,
				CreatedAt:   time.Unix(createdAt, 0),
				UpdatedAt:   time.Unix(updatedAt, 0),
			}
			speakerMap[speakerID] = info
		}

		info.SampleCount++

		// Cập nhật thời gian tạo sớm nhất và thời gian cập nhật mới nhất
		if createdAt > 0 {
			pointCreatedAt := time.Unix(createdAt, 0)
			if info.CreatedAt.IsZero() || pointCreatedAt.Before(info.CreatedAt) {
				info.CreatedAt = pointCreatedAt
			}
		}
		if updatedAt > 0 {
			pointUpdatedAt := time.Unix(updatedAt, 0)
			if info.UpdatedAt.IsZero() || pointUpdatedAt.After(info.UpdatedAt) {
				info.UpdatedAt = pointUpdatedAt
			}
		}
	}

	// Chuyển đổi thành lát
	speakers := make([]*SpeakerInfo, 0, len(speakerMap))
	for _, info := range speakerMap {
		speakers = append(speakers, info)
	}

	return speakers, nil
}

// DeleteByFilters xóa tất cả vectơ của loa (thông qua điều kiện lọc)
// Triển khai giao diện VectorDatabase
func (db *QdrantVectorDB) DeleteByFilters(uid, agentID, speakerID string) error {
	ctx := context.Background()

	// Nhận tất cả các điểm phù hợp bằng API cuộn
	conditions := []*qdrant.Condition{
		qdrant.NewMatch("uid", uid),
		qdrant.NewMatch("speaker_id", speakerID),
	}
	// Nếu ID tác nhân không trống, hãy thêm vào điều kiện lọc
	if agentID != "" {
		conditions = append(conditions, qdrant.NewMatch("agent_id", agentID))
	}
	filter := &qdrant.Filter{
		Must: conditions,
	}

	limit := uint32(10000)
	scrollResult, err := db.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: db.collectionName,
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(false), // Không cần tải trọng
	})
	if err != nil {
		return fmt.Errorf("failed to scroll points: %v", err)
	}

	if len(scrollResult) == 0 {
		return nil // Không có dữ liệu cần phải xóa
	}

	// Trích xuất tất cả ID điểm
	ids := make([]*qdrant.PointId, 0, len(scrollResult))
	for _, point := range scrollResult {
		ids = append(ids, point.Id)
	}

	// Xóa những điểm này
	_, err = db.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: db.collectionName,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Points{
				Points: &qdrant.PointsIdsList{
					Ids: ids,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete points: %v", err)
	}

	return nil
}

// DeleteByUUID xóa tất cả vectơ của người nói bằng UUID
// Triển khai giao diện VectorDatabase
func (db *QdrantVectorDB) DeleteByUUID(uid, agentID, uuid string) error {
	ctx := context.Background()

	// Sử dụng API cuộn để nhận tất cả các điểm phù hợp (được lọc theo uuid)
	conditions := []*qdrant.Condition{
		qdrant.NewMatch("uid", uid),
		qdrant.NewMatch("uuid", uuid),
	}
	// Nếu ID tác nhân không trống, hãy thêm vào điều kiện lọc
	if agentID != "" {
		conditions = append(conditions, qdrant.NewMatch("agent_id", agentID))
	}
	filter := &qdrant.Filter{
		Must: conditions,
	}

	limit := uint32(10000)
	scrollResult, err := db.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: db.collectionName,
		Filter:         filter,
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(false), // Không cần tải trọng
	})
	if err != nil {
		return fmt.Errorf("failed to scroll points: %v", err)
	}

	if len(scrollResult) == 0 {
		return fmt.Errorf("speaker with uuid %s not found for uid %s", uuid, uid)
	}

	// Trích xuất tất cả ID điểm
	ids := make([]*qdrant.PointId, 0, len(scrollResult))
	for _, point := range scrollResult {
		ids = append(ids, point.Id)
	}

	// Xóa những điểm này
	_, err = db.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: db.collectionName,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Points{
				Points: &qdrant.PointsIdsList{
					Ids: ids,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete points: %v", err)
	}

	return nil
}

// Đóng Đóng kết nối cơ sở dữ liệu vector
func (db *QdrantVectorDB) Close() error {
	// Qdrant Go Client có thể không cần phải đóng một cách rõ ràng nhưng giao diện vẫn được giữ lại để mở rộng trong tương lai
	return nil
}

// parsQdrantAddress phân tích địa chỉ Qdrant (định dạng: máy chủ: cổng hoặc máy chủ)
func parseQdrantAddress(addr string) (string, int) {
	host := "localhost"
	port := 6334

	if addr == "" {
		return host, port
	}

	parts := strings.Split(addr, ":")
	if len(parts) == 2 {
		host = parts[0]
		if p, err := strconv.Atoi(parts[1]); err == nil {
			port = p
		}
	} else if len(parts) == 1 {
		host = parts[0]
	}

	return host, port
}
