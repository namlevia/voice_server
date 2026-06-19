package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/gorilla/websocket"
)

var (
	baseURL      = "http://127.0.0.1:9000"
	speakerAPI   = baseURL + "/api/v1/speaker"
	speakerWSURL = "ws://127.0.0.1:9000/api/v1/speaker/identify_ws"
)

const (
	speakerID      = "test_speaker_001"
	speakerName    = "loa thử nghiệm"
	defaultUID     = "test_user_001"
	defaultAgentID = "test_agent_001"
	defaultUUID    = "test_uuid_001"
)

// Xác địnhResult xác định cấu trúc kết quả
type IdentifyResult struct {
	Identified  bool    `json:"identified"`
	SpeakerID   string  `json:"speaker_id"`
	SpeakerName string  `json:"speaker_name"`
	Confidence  float32 `json:"confidence"`
	Threshold   float32 `json:"threshold"`
}

// Cấu trúc phản hồi đăng ký RegisterResponse
type RegisterResponse struct {
	Message     string `json:"message"`
	UID         string `json:"uid"`
	SpeakerID   string `json:"speaker_id"`
	SpeakerName string `json:"speaker_name"`
}

// Cấu trúc phản hồi lỗi ErrorResponse
type ErrorResponse struct {
	Error string `json:"error"`
}

// Cấu trúc thông tin loa SpeakInfo
type SpeakerInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SampleCount int    `json:"sample_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Cấu trúc phản hồi danh sách ListResponse
type ListResponse struct {
	UID      string        `json:"uid"`
	Speakers []SpeakerInfo `json:"speakers"`
	Total    int           `json:"total"`
}

// DeleteResponse xóa cấu trúc phản hồi
type DeleteResponse struct {
	Message   string `json:"message"`
	UID       string `json:"uid"`
	SpeakerID string `json:"speaker_id"`
}

func main() {
	// Phân tích các tham số dòng lệnh
	var registerFile string
	var identifyFile string
	var listSpeakers bool
	var deleteSpeakerID string
	var customSpeakerID string
	var customSpeakerName string
	var customUID string
	var customAgentID string
	var customUUID string
	var maxFrames int
	var threshold float64
	var peekStartMs int
	var peekIntervalMs int
	var customBaseURL string
	var customSpeakerWSURL string

	flag.StringVar(&registerFile, "register", "", "Đường dẫn file âm thanh để đăng ký voiceprint (định dạng WAV)")
	flag.StringVar(&identifyFile, "identify", "", "Đường dẫn tệp âm thanh để nhận dạng giọng nói (định dạng WAV)")
	flag.BoolVar(&listSpeakers, "list", false, "Liệt kê tất cả các giọng nói đã đăng ký")
	flag.StringVar(&deleteSpeakerID, "delete", "", "Xóa giọng nói của ID người nói được chỉ định")
	flag.StringVar(&customSpeakerID, "speaker-id", speakerID, "ID người phát biểu (mặc định: test_loa_001)")
	flag.StringVar(&customSpeakerName, "speaker-name", speakerName, "Tên người phát biểu (mặc định: người phát biểu thử nghiệm)")
	flag.StringVar(&customUID, "uid", defaultUID, "ID người dùng (mặc định: test_user_001)")
	flag.StringVar(&customAgentID, "agent-id", defaultAgentID, "ID tác nhân (mặc định: test_agent_001, được yêu cầu ở phía máy chủ)")
	flag.StringVar(&customUUID, "uuid", defaultUUID, "Đăng ký phiên UUID (mặc định: test_uuid_001, được yêu cầu ở phía máy chủ)")
	flag.IntVar(&maxFrames, "frames", 0, "Số khung được gửi trong quá trình nhận dạng phát trực tuyến (0 nghĩa là gửi tất cả khung, tùy chọn)")
	flag.Float64Var(&threshold, "threshold", 0, "Ngưỡng nhận dạng (được sử dụng khi >0, nếu không thì giá trị mặc định của máy chủ sẽ được sử dụng, tùy chọn)")
	flag.IntVar(&peekStartMs, "peek-start-ms", 1200, "Thời lượng âm thanh tích lũy của lần xem nhanh đầu tiên được gửi trong tính năng nhận dạng phát trực tuyến (mili giây, <=0 có nghĩa là không có lần xem nhanh nào được gửi)")
	flag.IntVar(&peekIntervalMs, "peek-interval-ms", 200, "Khoảng thời gian gửi xem nhanh trong nhận dạng phát trực tuyến (mili giây, <=0 có nghĩa là chỉ gửi xem nhanh một lần)")
	flag.StringVar(&customBaseURL, "base-url", baseURL, "Địa chỉ HTTP dịch vụ (ví dụ: http://127.0.0.1:9000)")
	flag.StringVar(&customSpeakerWSURL, "speaker-ws-url", "", "Địa chỉ WS của Voiceprint (để trống để tự động lấy địa chỉ dựa trên url cơ sở)")
	flag.Parse()

	baseURL = strings.TrimRight(customBaseURL, "/")
	speakerAPI = baseURL + "/api/v1/speaker"
	if customSpeakerWSURL != "" {
		speakerWSURL = customSpeakerWSURL
	} else {
		switch {
		case strings.HasPrefix(baseURL, "https://"):
			speakerWSURL = "wss://" + strings.TrimPrefix(baseURL, "https://") + "/api/v1/speaker/identify_ws"
		case strings.HasPrefix(baseURL, "http://"):
			speakerWSURL = "ws://" + strings.TrimPrefix(baseURL, "http://") + "/api/v1/speaker/identify_ws"
		default:
			speakerWSURL = "ws://" + baseURL + "/api/v1/speaker/identify_ws"
		}
	}

	fmt.Println("========================================")
	fmt.Println("Chương trình kiểm tra nhận dạng giọng nói")
	fmt.Println("========================================")
	fmt.Printf("Địa chỉ dịch vụ: %s\n", baseURL)
	fmt.Printf("Địa chỉ WS giọng nói: %s\n", speakerWSURL)

	// Nếu tất cả các tham số không được chỉ định, hướng dẫn hiển thị
	if registerFile == "" && identifyFile == "" && !listSpeakers && deleteSpeakerID == "" {
		fmt.Println("\nCách sử dụng:")
		fmt.Println("hãy chạy test_loa.go -register <tập tin đăng ký>")
		fmt.Println("hãy chạy test_loa.go -identify <xác định tập tin>")
		fmt.Println("  go run test_speaker.go -list")
		fmt.Println("hãy chạy test_loa.go -xóa <ID loa>")
		fmt.Println("hãy chạy test_loa.go -đăng ký <tệp đăng ký> -xác định <tệp nhận dạng>")
		fmt.Println("\nMô tả tham số:")
		fmt.Println("-register <đường dẫn tệp> Đăng ký tệp âm thanh giọng nói (định dạng WAV, tùy chọn)")
		fmt.Println("-identify <đường dẫn tệp> Xác định tệp âm thanh của giọng nói (định dạng WAV, tùy chọn)")
		fmt.Println("-list Liệt kê tất cả các giọng nói đã đăng ký (tùy chọn)")
		fmt.Println("-delete <ID loa> Xóa giọng nói của ID loa được chỉ định (tùy chọn)")
		fmt.Println("-loa-id <ID> ID loa (tùy chọn, mặc định: test_loa_001)")
		fmt.Println("-tên người nói <tên> Tên người nói (tùy chọn, mặc định: người nói thử)")
		fmt.Println("-uid <user ID> ID người dùng (tùy chọn, mặc định: test_user_001)")
		fmt.Println("-agent-id <agent ID> ID đại lý (tùy chọn, mặc định: test_agent_001)")
		fmt.Println("-uuid <UUID> Phiên đăng ký UUID (tùy chọn, mặc định: test_uuid_001, được máy chủ yêu cầu trong quá trình đăng ký)")
		fmt.Println("-frames <số khung> Số lượng khung được gửi trong quá trình nhận dạng phát trực tuyến (0 có nghĩa là gửi tất cả các khung, tùy chọn)")
		fmt.Println("-threshold <threshold> Ngưỡng nhận dạng (được sử dụng khi >0, nếu không thì giá trị mặc định của máy chủ sẽ được sử dụng, tùy chọn)")
		fmt.Println("-peek-start-ms <milliseconds> Truyền phát xác định điểm thời gian xem nhanh đầu tiên (<=0 không gửi xem nhanh)")
		fmt.Println("-peek-interval-ms <mili giây> khoảng thời gian gửi xem nhanh (<=0 chỉ gửi xem nhanh một lần)")
		fmt.Println("-base-url <URL> Địa chỉ HTTP dịch vụ (mặc định: http://127.0.0.1:9000)")
		fmt.Println("-loa-ws-url <URL> Địa chỉ Voiceprint WS (được lấy tự động theo url cơ sở theo mặc định)")
		fmt.Println("\nVí dụ:")
		fmt.Println("# Chỉ đăng ký giọng nói")
		fmt.Println("  go run test_speaker.go -register register.wav")
		fmt.Println("# Chỉ nhận dạng giọng nói")
		fmt.Println("  go run test_speaker.go -identify identify.wav")
		fmt.Println("# Liệt kê tất cả các giọng nói")
		fmt.Println("  go run test_speaker.go -list")
		fmt.Println("# Xóa dấu giọng nói")
		fmt.Println("  go run test_speaker.go -delete test_speaker_001")
		fmt.Println("# Đăng ký và xác định")
		fmt.Println("  go run test_speaker.go -register register.wav -identify identify.wav")
		fmt.Println("  go run test_speaker.go -register test.wav -identify test.wav -speaker-id user001 -uid user001")
		fmt.Println("# Chỉ gửi 10 khung hình đầu tiên trong quá trình nhận dạng phát trực tuyến")
		fmt.Println("  go run test_speaker.go -identify test.wav -frames 10")
		fmt.Println("# Sử dụng nhận dạng ngưỡng tùy chỉnh")
		fmt.Println("  go run test_speaker.go -identify test.wav -threshold 0.7")
		fmt.Println("# Đạt được kết quả trung gian trong nhận dạng phát trực tuyến (peek)")
		fmt.Println("  go run test_speaker.go -identify test.wav -peek-start-ms 1200 -peek-interval-ms 200")
		fmt.Println("# Chỉ định địa chỉ dịch vụ thử nghiệm")
		fmt.Println("  go run test_speaker.go -identify test.wav -base-url http://127.0.0.1:9000")
		os.Exit(1)
	}

	// Xử lý truy vấn danh sách
	if listSpeakers {
		fmt.Printf("\nBước 1: Lấy danh sách giọng nói (ID người dùng: %s", customUID)
		if customAgentID != "" {
			fmt.Printf(", ID đại lý: %s", customAgentID)
		}
		fmt.Println(")...")
		if err := listSpeakersFunc(customUID, customAgentID); err != nil {
			fmt.Printf("❌ Không lấy được danh sách: %v\n", err)
			os.Exit(1)
		}
		// Nếu bạn chỉ thực hiện truy vấn danh sách, hãy thoát trực tiếp
		if registerFile == "" && identifyFile == "" && deleteSpeakerID == "" {
			return
		}
	}

	// Xử lý việc xóa
	if deleteSpeakerID != "" {
		stepNum := 1
		if listSpeakers {
			stepNum = 2
		}
		fmt.Printf("\nBước %d: Xóa giọng nói (ID người nói: %s, ID người dùng: %s", stepNum, deleteSpeakerID, customUID)
		if customAgentID != "" {
			fmt.Printf(", ID đại lý: %s", customAgentID)
		}
		fmt.Println(")...")
		if err := deleteSpeaker(deleteSpeakerID, customUID, customAgentID); err != nil {
			fmt.Printf("❌ Xóa không thành công: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ Xóa thành công")
		// Nếu bạn chỉ thực hiện thao tác xóa, hãy thoát trực tiếp
		if registerFile == "" && identifyFile == "" {
			return
		}
	}

	// Quy trình đăng ký
	if registerFile != "" {
		// Kiểm tra file đăng ký có tồn tại không
		registerPath, err := filepath.Abs(registerFile)
		if err != nil {
			fmt.Printf("❌ Lỗi: Không giải quyết được đường dẫn file đăng ký: %v\n", err)
			os.Exit(1)
		}
		if _, err := os.Stat(registerPath); os.IsNotExist(err) {
			fmt.Printf("❌ Lỗi: Không tìm thấy file đăng ký %s\n", registerPath)
			os.Exit(1)
		}
		fmt.Printf("✅ Đã tìm thấy file âm thanh đã đăng ký: %s\n", registerPath)

		// Tính số bước
		stepNum := 1
		if listSpeakers {
			stepNum++
		}
		if deleteSpeakerID != "" {
			stepNum++
		}

		// Đăng ký giọng nói
		fmt.Printf("\nBước %d: Đăng ký giọng nói (sử dụng tệp: %s)...\n", stepNum, filepath.Base(registerPath))
		if err := registerSpeaker(registerPath, customSpeakerID, customSpeakerName, customUID, customAgentID, customUUID); err != nil {
			fmt.Printf("❌ Đăng ký không thành công: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ Đăng ký thành công")

		// Đợi một lát để đảm bảo dữ liệu đã được lưu
		time.Sleep(500 * time.Millisecond)

		// Nếu chỉ thực hiện thao tác đăng ký thì thoát trực tiếp
		if identifyFile == "" {
			return
		}
	}

	// nhận dạng quá trình
	if identifyFile != "" {
		// Kiểm tra xem tập tin nhận dạng có tồn tại không
		identifyPath, err := filepath.Abs(identifyFile)
		if err != nil {
			fmt.Printf("❌ Lỗi: Không thể giải quyết đường dẫn file được nhận dạng: %v\n", err)
			os.Exit(1)
		}
		if _, err := os.Stat(identifyPath); os.IsNotExist(err) {
			fmt.Printf("❌ Lỗi: Không tìm thấy file nhận dạng %s\n", identifyPath)
			os.Exit(1)
		}
		fmt.Printf("✅ Đã tìm thấy tệp âm thanh được nhận dạng: %s\n", identifyPath)

		// Tính số bước
		stepNum := 1
		if listSpeakers {
			stepNum++
		}
		if deleteSpeakerID != "" {
			stepNum++
		}
		if registerFile != "" {
			stepNum++
		}

		// Nhận dạng giọng nói HTTP
		fmt.Printf("\nBước %d: Nhận dạng giọng nói HTTP (sử dụng tệp: %s", stepNum, filepath.Base(identifyPath))
		if threshold > 0 {
			fmt.Printf(", ngưỡng: %.4f", threshold)
		}
		fmt.Println(")...")
		result, err := identifySpeaker(identifyPath, customUID, customAgentID, threshold)
		if err != nil {
			fmt.Printf("❌ Nhận dạng không thành công: %v\n", err)
			os.Exit(1)
		}

		// Hiển thị kết quả nhận dạng HTTP
		fmt.Println("\nKết quả nhận dạng HTTP:")
		fmt.Println("========================================")
		fmt.Printf("Trạng thái nhận dạng: %v\n", result.Identified)
		if result.Identified {
			fmt.Printf("ID người nói: %s\n", result.SpeakerID)
			fmt.Printf("Tên người phát biểu: %s\n", result.SpeakerName)
			fmt.Printf("Điểm tương đồng: %.4f\n", result.Confidence)
			fmt.Printf("Ngưỡng: %.4f\n", result.Threshold)
			if result.Confidence >= result.Threshold {
				fmt.Println("✅ Nhận dạng thành công, độ tương đồng vượt ngưỡng")
			} else {
				fmt.Println("⚠️ Nhận dạng thành công nhưng độ tương đồng thấp hơn ngưỡng")
			}
		} else {
			fmt.Println("❌ Không tìm thấy loa phù hợp")
		}
		fmt.Println("========================================")

		// Nhận dạng phát trực tuyến WebSocket
		stepNum++
		fmt.Printf("\nBước %d: Nhận dạng phát trực tuyến WebSocket (Sử dụng tệp: %s", stepNum, filepath.Base(identifyPath))
		if maxFrames > 0 {
			fmt.Printf(", đang gửi %d khung hình đầu tiên", maxFrames)
		}
		if threshold > 0 {
			fmt.Printf(", ngưỡng: %.4f", threshold)
		}
		if peekStartMs > 0 {
			fmt.Printf(", bắt đầu xem nhanh: %dms, khoảng thời gian xem nhanh: %dms", peekStartMs, peekIntervalMs)
		} else {
			fmt.Printf(", nhìn trộm: đóng")
		}
		fmt.Println(")...")
		wsResult, err := identifySpeakerWebSocket(identifyPath, customUID, customAgentID, maxFrames, threshold, peekStartMs, peekIntervalMs)
		if err != nil {
			fmt.Printf("❌ Nhận dạng WebSocket không thành công: %v\n", err)
			os.Exit(1)
		}

		// Hiển thị kết quả nhận dạng WebSocket
		fmt.Println("\nKết quả nhận dạng phát trực tuyếnWebSocket:")
		fmt.Println("========================================")
		fmt.Printf("Trạng thái nhận dạng: %v\n", wsResult.Identified)
		if wsResult.Identified {
			fmt.Printf("ID người nói: %s\n", wsResult.SpeakerID)
			fmt.Printf("Tên người phát biểu: %s\n", wsResult.SpeakerName)
			fmt.Printf("Điểm tương đồng: %.4f\n", wsResult.Confidence)
			fmt.Printf("Ngưỡng: %.4f\n", wsResult.Threshold)
			if wsResult.Confidence >= wsResult.Threshold {
				fmt.Println("✅ Nhận dạng thành công, độ tương đồng vượt ngưỡng")
			} else {
				fmt.Println("⚠️ Nhận dạng thành công nhưng độ tương đồng thấp hơn ngưỡng")
			}
		} else {
			fmt.Println("❌ Không tìm thấy loa phù hợp")
		}
		fmt.Println("========================================")
	}
}

// registerLoa đăng ký giọng nói
func registerSpeaker(wavPath string, sid string, sname string, uid string, agentID string, uuid string) error {
	// mở tập tin
	file, err := os.Open(wavPath)
	if err != nil {
		return fmt.Errorf("Không mở được tập tin: %v", err)
	}
	defer file.Close()

	// Tạo trình soạn thảo nhiều phần
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Thêm trường biểu mẫu
	if err := writer.WriteField("uid", uid); err != nil {
		return fmt.Errorf("Không thể ghi uid: %v", err)
	}

	if agentID != "" {
		if err := writer.WriteField("agent_id", agentID); err != nil {
			return fmt.Errorf("Không ghi được Agent_id: %v", err)
		}
	}

	if uuid != "" {
		if err := writer.WriteField("uuid", uuid); err != nil {
			return fmt.Errorf("Không thể ghi uuid: %v", err)
		}
	}

	if err := writer.WriteField("speaker_id", sid); err != nil {
		return fmt.Errorf("Không ghi được loa_id: %v", err)
	}

	if err := writer.WriteField("speaker_name", sname); err != nil {
		return fmt.Errorf("Không ghi được tên loa: %v", err)
	}

	// Thêm tập tin
	part, err := writer.CreateFormFile("audio", filepath.Base(wavPath))
	if err != nil {
		return fmt.Errorf("Không tạo được trường tệp: %v", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("Không sao chép được nội dung tập tin: %v", err)
	}

	// Đóng nhà văn
	if err := writer.Close(); err != nil {
		return fmt.Errorf("Không đóng được trình ghi: %v", err)
	}

	// Tạo yêu cầu HTTP
	req, err := http.NewRequest("POST", speakerAPI+"/register", &requestBody)
	if err != nil {
		return fmt.Errorf("Tạo yêu cầu không thành công: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-User-ID", uid) // Đồng thời chuyển uid qua tiêu đề yêu cầu
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID) // Đồng thời chuyển Agent_id qua tiêu đề yêu cầu
	}

	// Gửi yêu cầu
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Không gửi được yêu cầu: %v", err)
	}
	defer resp.Body.Close()

	// Đọc phản hồi
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Không đọc được phản hồi: %v", err)
	}

	// Kiểm tra mã trạng thái
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, errResp.Error)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Phân tích phản hồi
	var registerResp RegisterResponse
	if err := json.Unmarshal(body, &registerResp); err != nil {
		return fmt.Errorf("Không phân tích được phản hồi: %v", err)
	}

	fmt.Printf("ID người dùng: %s\n", uid)
	fmt.Printf("ID đăng ký: %s\n", registerResp.SpeakerID)
	fmt.Printf("Tên đã đăng ký: %s\n", registerResp.SpeakerName)

	return nil
}

// nhận dạngSpeaker nhận dạng giọng nói
// ngưỡng: ngưỡng nhận dạng, nếu <= 0 thì sử dụng giá trị mặc định của máy chủ
func identifySpeaker(wavPath string, uid string, agentID string, threshold float64) (*IdentifyResult, error) {
	// mở tập tin
	file, err := os.Open(wavPath)
	if err != nil {
		return nil, fmt.Errorf("Không mở được tập tin: %v", err)
	}
	defer file.Close()

	// Tạo trình soạn thảo nhiều phần
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Thêm uid trường biểu mẫu
	if err := writer.WriteField("uid", uid); err != nil {
		return nil, fmt.Errorf("Không thể ghi uid: %v", err)
	}

	// Thêm trường biểu mẫu Agent_id (nếu được cung cấp)
	if agentID != "" {
		if err := writer.WriteField("agent_id", agentID); err != nil {
			return nil, fmt.Errorf("Không ghi được Agent_id: %v", err)
		}
	}

	// Thêm ngưỡng trường biểu mẫu (nếu được cung cấp và > 0)
	if threshold > 0 {
		if err := writer.WriteField("threshold", fmt.Sprintf("%.6f", threshold)); err != nil {
			return nil, fmt.Errorf("Không ghi được ngưỡng: %v", err)
		}
	}

	// Thêm tập tin
	part, err := writer.CreateFormFile("audio", filepath.Base(wavPath))
	if err != nil {
		return nil, fmt.Errorf("Không tạo được trường tệp: %v", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("Không sao chép được nội dung tập tin: %v", err)
	}

	// Đóng nhà văn
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("Không đóng được trình ghi: %v", err)
	}

	// Tạo yêu cầu HTTP
	req, err := http.NewRequest("POST", speakerAPI+"/identify", &requestBody)
	if err != nil {
		return nil, fmt.Errorf("Tạo yêu cầu không thành công: %v", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-User-ID", uid) // Đồng thời chuyển uid qua tiêu đề yêu cầu
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID) // Đồng thời chuyển Agent_id qua tiêu đề yêu cầu
	}

	// Gửi yêu cầu
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Không gửi được yêu cầu: %v", err)
	}
	defer resp.Body.Close()

	// Đọc phản hồi
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Không đọc được phản hồi: %v", err)
	}

	// Kiểm tra mã trạng thái
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Phân tích phản hồi
	var result IdentifyResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("Không phân tích được phản hồi: %v", err)
	}

	return &result, nil
}

// readWavToFloat32 đọc các tệp WAV và chuyển đổi chúng thành mảng float32
func readWavToFloat32(wavPath string) ([]float32, int, error) {
	// mở tập tin
	file, err := os.Open(wavPath)
	if err != nil {
		return nil, 0, fmt.Errorf("Không mở được tập tin: %v", err)
	}
	defer file.Close()

	// Tạo bộ giải mã WAV
	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return nil, 0, fmt.Errorf("Tệp WAV không hợp lệ")
	}

	// Đọc thông tin tập tin WAV
	decoder.ReadInfo()
	format := decoder.Format()
	sampleRate := int(format.SampleRate)
	numChannels := int(format.NumChannels)

	// Đọc tất cả dữ liệu PCM
	var allSamples []float32

	// Đọc bằng bộ đệm
	frameSize := sampleRate * 20 / 1000 // khung 20ms
	audioBuf := &audio.IntBuffer{
		Format:         format,
		SourceBitDepth: 16,
		Data:           make([]int, frameSize*numChannels),
	}

	for {
		n, err := decoder.PCMBuffer(audioBuf)
		if err == io.EOF || n == 0 {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("Không đọc được dữ liệu WAV: %v", err)
		}

		// Chuyển đổi sang định dạng float32 (phạm vi [-1.0, 1.0])
		for i := 0; i < n; i++ {
			sample := float32(audioBuf.Data[i]) / 32767.0
			allSamples = append(allSamples, sample)
		}
	}

	// Nếu là âm thanh nổi, hãy chuyển sang đơn âm (trung bình)
	if numChannels == 2 {
		monoSamples := make([]float32, len(allSamples)/2)
		for i := 0; i < len(monoSamples); i++ {
			monoSamples[i] = (allSamples[i*2] + allSamples[i*2+1]) / 2.0
		}
		allSamples = monoSamples
	}

	return allSamples, sampleRate, nil
}

// float32ToBytes chuyển đổi mảng float32 thành byte nhị phân (endian nhỏ)
func float32ToBytes(samples []float32) []byte {
	buf := make([]byte, len(samples)*4)
	for i, sample := range samples {
		// Chuyển đổi float32 thành byte (sử dụng math.Float32bits)
		bits := math.Float32bits(sample)
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf
}

// nhận dạngSpeakerWebSocket Truyền nhận dạng giọng nói thông qua WebSocket
// maxFrames: Số lượng khung hình tối đa cần gửi, 0 nghĩa là gửi tất cả các khung hình
// ngưỡng: ngưỡng nhận dạng, nếu <= 0 thì sử dụng giá trị mặc định của máy chủ
// eekStartMs: thời gian xem trước lần đầu tiên (mili giây, <=0 có nghĩa là không có bản xem trước nào được gửi)
// eekIntervalMs: khoảng thời gian xem nhanh (mili giây, <=0 có nghĩa là chỉ gửi xem nhanh một lần)
func identifySpeakerWebSocket(wavPath string, uid string, agentID string, maxFrames int, threshold float64, peekStartMs int, peekIntervalMs int) (*IdentifyResult, error) {
	// Đọc tập tin WAV
	audioData, sampleRate, err := readWavToFloat32(wavPath)
	if err != nil {
		return nil, fmt.Errorf("Không đọc được tập tin âm thanh: %v", err)
	}

	fmt.Printf("Tốc độ mẫu âm thanh: %d Hz\n", sampleRate)
	fmt.Printf("Số lượng mẫu âm thanh: %d\n", len(audioData))
	fmt.Printf("Thời lượng âm thanh: %.2f giây\n", float64(len(audioData))/float64(sampleRate))
	fmt.Printf("Lưu ý: Máy khách không thực hiện lấy mẫu lại và máy chủ sẽ tự động lấy mẫu lại theo tốc độ lấy mẫu mà mô hình mong đợi\n")

	// Kết nối WebSocket, chuyển tốc độ lấy mẫu ban đầu và uid
	// Máy chủ sẽ tự động lấy mẫu lại theo tốc độ lấy mẫu mà kiểu máy mong đợi (thường là 16000Hz) dựa trên tốc độ lấy mẫu đến.
	wsURL := fmt.Sprintf("%s?sample_rate=%d&uid=%s", speakerWSURL, sampleRate, uid)
	if agentID != "" {
		wsURL += fmt.Sprintf("&agent_id=%s", url.QueryEscape(agentID))
	}
	if threshold > 0 {
		wsURL += fmt.Sprintf("&threshold=%.6f", threshold)
	}

	// Tạo tiêu đề yêu cầu và chuyển uid và Agent_id qua tiêu đề yêu cầu
	header := http.Header{}
	header.Set("X-User-ID", uid)
	if agentID != "" {
		header.Set("X-Agent-ID", agentID)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("Kết nối WebSocket không thành công: %v", err)
	}
	defer conn.Close()

	// Đặt thời gian chờ đọc
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Nhận tin nhắn xác nhận kết nối
	var connectionMsg map[string]interface{}
	if err := conn.ReadJSON(&connectionMsg); err != nil {
		return nil, fmt.Errorf("Không đọc được thông báo xác nhận kết nối: %v", err)
	}
	if msgType, ok := connectionMsg["type"].(string); !ok || msgType != "connection" {
		return nil, fmt.Errorf("Thông báo kết nối không mong muốn: %v", connectionMsg)
	}
	fmt.Printf("✅ Kết nối WebSocket thành công\n")

	// Gửi dữ liệu âm thanh theo từng đoạn (~20ms mỗi đoạn)
	chunkSize := sampleRate * 20 / 1000 // Số lượng mẫu trong 20ms
	totalChunks := (len(audioData) + chunkSize - 1) / chunkSize

	// Nếu số lượng khung tối đa được chỉ định, hãy giới hạn số lượng khối được gửi
	chunksToSend := totalChunks
	if maxFrames > 0 && maxFrames < totalChunks {
		chunksToSend = maxFrames
		fmt.Printf("Bắt đầu gửi dữ liệu âm thanh (gửi %d/%d đoạn đầu tiên, khoảng %d mẫu mỗi đoạn)...\n", chunksToSend, totalChunks, chunkSize)
	} else {
		fmt.Printf("Bắt đầu gửi dữ liệu âm thanh (theo %d đoạn, khoảng %d mẫu mỗi đoạn)...\n", totalChunks, chunkSize)
	}

	// cấu hình lập lịch xem nhanh (được kích hoạt bởi thời lượng gửi âm thanh tích lũy)
	peekEnabled := peekStartMs > 0
	peekStartSamples := sampleRate * peekStartMs / 1000
	peekIntervalSamples := sampleRate * peekIntervalMs / 1000
	if peekEnabled {
		if peekStartSamples <= 0 {
			peekStartSamples = 1
		}
		if peekIntervalSamples <= 0 {
			peekIntervalSamples = 0 // Chỉ gửi một lần
		}
		fmt.Printf("🔎 Đã bật Peek: start=%dms, interval=%dms\n", peekStartMs, peekIntervalMs)
	}
	nextPeekSamples := peekStartSamples
	peekSeq := 0

	// Bắt đầu goroutine để nhận tin nhắn
	resultChan := make(chan *IdentifyResult, 1)
	errorChan := make(chan error, 1)

	go func() {
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					errorChan <- fmt.Errorf("Lỗi đọc WebSocket: %v", err)
				}
				return
			}

			if messageType == websocket.TextMessage {
				var msg map[string]interface{}
				if err := json.Unmarshal(message, &msg); err != nil {
					fmt.Printf("⚠️Không thể phân tích tin nhắn: %v\n", err)
					continue
				}

				if msgType, ok := msg["type"].(string); ok {
					switch msgType {
					case "audio_received":
						// Xác nhận tiếp nhận âm thanh
						if samples, ok := msg["samples"].(float64); ok {
							fmt.Printf("📦 Máy chủ xác nhận đã nhận được %d mẫu\n", int(samples))
						}
						continue
					case "partial_result":
						requestID := getString(msg, "request_id")
						throttled := getBool(msg, "throttled")
						round := 0
						if v, ok := msg["round"].(float64); ok {
							round = int(v)
						}
						audioMs := 0.0
						if v, ok := msg["audio_ms"].(float64); ok {
							audioMs = v
						}
						if throttled {
							fmt.Printf("⏱️ nhìn trộm(%s) bị giới hạn, round=%d, audio=%.0fms\n", requestID, round, audioMs)
							continue
						}
						if resultData, ok := msg["result"].(map[string]interface{}); ok {
							identified := getBool(resultData, "identified")
							if identified {
								fmt.Printf("   🔍 peek(%s): round=%d, audio=%.0fms, speaker=%s(%s), conf=%.4f, th=%.4f\n",
									requestID, round, audioMs,
									getString(resultData, "speaker_name"),
									getString(resultData, "speaker_id"),
									getFloat32(resultData, "confidence"),
									getFloat32(resultData, "threshold"),
								)
							} else {
								fmt.Printf("🔍 nhìn lén(%s): round=%d, audio=%.0fms, chưa xác định được người nói phù hợp\n", requestID, round, audioMs)
							}
						} else if errMsg, ok := msg["error"].(string); ok {
							fmt.Printf("⚠️eek(%s) trả về lỗi: %s\n", requestID, errMsg)
						}
						continue
					case "result":
						if resultData, ok := msg["result"].(map[string]interface{}); ok {
							result := &IdentifyResult{
								Identified:  getBool(resultData, "identified"),
								SpeakerID:   getString(resultData, "speaker_id"),
								SpeakerName: getString(resultData, "speaker_name"),
								Confidence:  getFloat32(resultData, "confidence"),
								Threshold:   getFloat32(resultData, "threshold"),
							}
							resultChan <- result
							return
						}
					case "error":
						if errMsg, ok := msg["message"].(string); ok {
							errorChan <- fmt.Errorf("Lỗi máy chủ: %s", errMsg)
							return
						}
					default:
						fmt.Printf("⚠️ Đã nhận được loại tin nhắn không xác định: %s, nội dung: %v\n", msgType, msg)
					}
				} else {
					fmt.Printf("⚠️ Ngoại lệ định dạng tin nhắn: %v\n", msg)
				}
			} else {
				fmt.Printf("⚠️ Đã nhận được tin nhắn không phải văn bản, gõ: %d\n", messageType)
			}
		}
	}()

	// Gửi khối dữ liệu âm thanh
	totalSamplesSent := 0
	currentChunk := 0
	for i := 0; i < len(audioData); i += chunkSize {
		// Nếu chỉ định số lượng khung tối đa, hãy kiểm tra xem đã đạt đến giới hạn chưa (kiểm tra trước khi gửi)
		if maxFrames > 0 && currentChunk >= maxFrames {
			fmt.Printf("⚠️ Đã đạt đến giới hạn khung hình tối đa (%d khung hình), dừng gửi\n", maxFrames)
			break
		}

		end := i + chunkSize
		if end > len(audioData) {
			end = len(audioData)
		}

		chunk := audioData[i:end]
		chunkBytes := float32ToBytes(chunk)
		totalSamplesSent += len(chunk)
		currentChunk++

		if err := conn.WriteMessage(websocket.BinaryMessage, chunkBytes); err != nil {
			return nil, fmt.Errorf("Không gửi được dữ liệu âm thanh: %v", err)
		}

		// Gửi yêu cầu xem trước theo lịch trình trong quá trình gửi (có thể nhiều lần)
		for peekEnabled && totalSamplesSent >= nextPeekSamples {
			peekSeq++
			requestID := fmt.Sprintf("peek_%d", peekSeq)
			peekCmd := map[string]interface{}{
				"action":     "peek",
				"request_id": requestID,
			}
			if err := conn.WriteJSON(peekCmd); err != nil {
				return nil, fmt.Errorf("Không gửi được lệnh xem nhanh: %v", err)
			}
			fmt.Printf("🔎 Yêu cầu xem trước %s đã được gửi (âm thanh tích lũy %.0fms)\n",
				requestID, float64(totalSamplesSent)/float64(sampleRate)*1000)

			// interval<=0 chỉ gửi bản tóm tắt một lần
			if peekIntervalSamples <= 0 {
				peekEnabled = false
				break
			}
			nextPeekSamples += peekIntervalSamples
		}

		// Hiển thị tiến trình gửi
		shouldPrint := currentChunk%10 == 0 || end == len(audioData) || (maxFrames > 0 && currentChunk >= maxFrames)
		if shouldPrint {
			fmt.Printf("Đã gửi %d/%d khối (tổng cộng %d mẫu)\n", currentChunk, chunksToSend, totalSamplesSent)
		}
	}

	if totalSamplesSent != len(audioData) {
		fmt.Printf("⚠️ Cảnh báo: Số lượng mẫu gửi (%d) không khớp với tổng số mẫu (%d)\n", totalSamplesSent, len(audioData))
	}

	fmt.Printf("✅ Đã hoàn tất gửi dữ liệu âm thanh\n")

	// Gửi lệnh hoàn thành
	finishCmd := map[string]interface{}{
		"action": "finish",
	}
	if err := conn.WriteJSON(finishCmd); err != nil {
		return nil, fmt.Errorf("Không gửi được lệnh hoàn thành: %v", err)
	}
	fmt.Printf("✅ Lệnh hoàn thành đã được gửi đi, đang chờ kết quả ghi nhận...\n")

	// Đang chờ kết quả
	select {
	case result := <-resultChan:
		// Hiển thị chi tiết nhận dạng
		if !result.Identified {
			fmt.Printf("⚠️ Nhận dạng không thành công: độ tương tự %.4f < ngưỡng %.4f\n", result.Confidence, result.Threshold)
		}
		return result, nil
	case err := <-errorChan:
		return nil, err
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("Thời gian chờ kết quả nhận dạng (15 giây)")
	}
}

// Chức năng trợ giúp: lấy giá trị từ bản đồ một cách an toàn
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getFloat32(m map[string]interface{}, key string) float32 {
	if v, ok := m[key].(float64); ok {
		return float32(v)
	}
	return 0.0
}

// listSpeakersFunc lấy danh sách giọng nói
func listSpeakersFunc(uid string, agentID string) error {
	// Xây dựng URL, mã hóa thông số an toàn
	apiURL, err := url.Parse(speakerAPI + "/list")
	if err != nil {
		return fmt.Errorf("Không thể phân tích URL: %v", err)
	}
	params := url.Values{}
	params.Set("uid", uid)
	if agentID != "" {
		params.Set("agent_id", agentID)
	}
	apiURL.RawQuery = params.Encode()

	// Tạo yêu cầu HTTP
	req, err := http.NewRequest("GET", apiURL.String(), nil)
	if err != nil {
		return fmt.Errorf("Tạo yêu cầu không thành công: %v", err)
	}

	req.Header.Set("X-User-ID", uid) // Đồng thời chuyển uid qua tiêu đề yêu cầu
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID) // Đồng thời chuyển Agent_id qua tiêu đề yêu cầu
	}

	// Gửi yêu cầu
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Không gửi được yêu cầu: %v", err)
	}
	defer resp.Body.Close()

	// Đọc phản hồi
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Không đọc được phản hồi: %v", err)
	}

	// Kiểm tra mã trạng thái
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, errResp.Error)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Phân tích phản hồi
	var listResp ListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return fmt.Errorf("Không phân tích được phản hồi: %v", err)
	}

	// Hiển thị kết quả
	fmt.Println("\nDanh sách dấu giọng nói:")
	fmt.Println("========================================")
	fmt.Printf("ID người dùng: %s\n", listResp.UID)
	fmt.Printf("Tổng cộng: %d\n", listResp.Total)

	if len(listResp.Speakers) == 0 {
		fmt.Println("\nChưa đăng ký giọng nói")
	} else {
		fmt.Println("\nDanh sách người phát biểu:")
		fmt.Println("----------------------------------------")
		for i, speaker := range listResp.Speakers {
			fmt.Printf("%d. ID người nói: %s\n", i+1, speaker.ID)
			fmt.Printf("Tên người phát biểu: %s\n", speaker.Name)
			fmt.Printf("Cỡ mẫu: %d\n", speaker.SampleCount)
			fmt.Printf("Thời gian tạo: %s\n", speaker.CreatedAt)
			fmt.Printf("Thời gian cập nhật: %s\n", speaker.UpdatedAt)
			if i < len(listResp.Speakers)-1 {
				fmt.Println()
			}
		}
	}
	fmt.Println("========================================")

	return nil
}

// deleteSpeaker xóa giọng nói
func deleteSpeaker(speakerID string, uid string, agentID string) error {
	// Xây dựng URL, mã hóa tham số đường dẫn an toàn
	apiURL, err := url.Parse(speakerAPI)
	if err != nil {
		return fmt.Errorf("Không thể phân tích URL: %v", err)
	}
	// Sử dụng PathEscape để mã hóa ID loa nhằm đảm bảo các ký tự đặc biệt được xử lý chính xác
	apiURL.Path += "/" + url.PathEscape(speakerID)
	params := url.Values{}
	params.Set("uid", uid)
	if agentID != "" {
		params.Set("agent_id", agentID)
	}
	apiURL.RawQuery = params.Encode()

	// Tạo một yêu cầu XÓA HTTP
	req, err := http.NewRequest("DELETE", apiURL.String(), nil)
	if err != nil {
		return fmt.Errorf("Tạo yêu cầu không thành công: %v", err)
	}

	req.Header.Set("X-User-ID", uid) // Đồng thời chuyển uid qua tiêu đề yêu cầu
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID) // Đồng thời chuyển Agent_id qua tiêu đề yêu cầu
	}

	// Gửi yêu cầu
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Không gửi được yêu cầu: %v", err)
	}
	defer resp.Body.Close()

	// Đọc phản hồi
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Không đọc được phản hồi: %v", err)
	}

	// Kiểm tra mã trạng thái
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, errResp.Error)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Phân tích phản hồi
	var deleteResp DeleteResponse
	if err := json.Unmarshal(body, &deleteResp); err != nil {
		return fmt.Errorf("Không phân tích được phản hồi: %v", err)
	}

	fmt.Printf("ID người dùng: %s\n", deleteResp.UID)
	fmt.Printf("ID người nói: %s\n", deleteResp.SpeakerID)
	fmt.Printf("Tin nhắn: %s\n", deleteResp.Message)

	return nil
}
