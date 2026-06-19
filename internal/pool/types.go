package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Giao diện nhóm tài nguyên nhóm - một giao diện thống nhất việc triển khai nhóm khác nhau
type Pool interface {
	SubmitTask(task *Task) error
	GetStats() map[string]interface{}
	Shutdown()
}

// Cấu trúc nhiệm vụ nhiệm vụ - dành cho StreamPool
type Task struct {
	ID         string
	SessionID  string
	Samples    []float32
	SampleRate int
	ResultChan chan *Result
	Callback   func(string, error)
	Context    context.Context
	Timeout    time.Duration // Hết thời gian thực hiện nhiệm vụ
	CreatedAt  time.Time     // Thời gian tạo nhiệm vụ
}

// Kết quả nhận dạng kết quả
type Result struct {
	Text      string
	Timestamp time.Time
	Error     error
}

// Thống kê nhóm PoolStats
type PoolStats struct {
	TasksSubmitted      int64
	TasksProcessed      int64
	TasksRejected       int64
	TotalProcessingTime int64 // nano giây
	MaxProcessingTime   int64 // nano giây
}

// NewPoolStats tạo một phiên bản thống kê mới
func NewPoolStats() *PoolStats {
	return &PoolStats{}
}

// Cấu trúc Worker Worker - giữ lại kiến ​​trúc đa phiên bản ban đầu
type Worker struct {
	ID         int
	recognizer *sherpa.OfflineRecognizer
	taskChan   chan *Task
	quit       chan bool
	wg         *sync.WaitGroup
	isActive   int32
}

// Định nghĩa lỗi
var (
	ErrPoolShutdown = fmt.Errorf("pool is shutdown")
	ErrQueueFull    = fmt.Errorf("task queue is full")
)

const (
	TEN_VAD_TYPE = "ten_vad"
	SILERO_TYPE  = "silero_vad"
)
