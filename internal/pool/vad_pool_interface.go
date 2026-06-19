package pool

// VADPoolInterface Giao diện VAD pool
type VADPoolInterface interface {
	// Khởi tạo nhóm khởi tạo
	Initialize() error

	// Nhận phiên bản VAD
	Get() (VADInstanceInterface, error)

	// Put trả về phiên bản VAD
	Put(instance VADInstanceInterface)

	// GetStatsNhận số liệu thống kê
	GetStats() map[string]interface{}

	// Tắt máy đóng hồ bơi
	Shutdown()
}

// VADInstanceInterface Giao diện phiên bản VAD
type VADInstanceInterface interface {
	// GetID Lấy ID phiên bản
	GetID() int

	// GetType lấy loại VAD
	GetType() string

	// IsInUse kiểm tra xem nó có được sử dụng không
	IsInUse() bool

	// SetInUse đặt trạng thái sử dụng
	SetInUse(inUse bool)

	// GetLastUsed Lấy thời gian sử dụng cuối cùng
	GetLastUsed() int64

	// SetLastUsed đặt thời gian sử dụng cuối cùng
	SetLastUsed(timestamp int64)

	// Đặt lại trạng thái đặt lại phiên bản
	Reset() error

	// Phá hủy phiên bản
	Destroy() error
}

// VADPoolFactory Giao diện nhà máy VAD pool
type VADPoolFactory interface {
	// CreatePool tạo nhóm VAD
	CreatePool(config interface{}) (VADPoolInterface, error)

	// GetSupportedTypes Nhận các loại VAD được hỗ trợ
	GetSupportedTypes() []string
}
