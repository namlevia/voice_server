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
	speakerName    = "测试说话人"
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

	flag.StringVar(&registerFile, "register", "", "注册声纹的音频文件路径（WAV格式）")
	flag.StringVar(&identifyFile, "identify", "", "识别声纹的音频文件路径（WAV格式）")
	flag.BoolVar(&listSpeakers, "list", false, "列出所有已注册的声纹")
	flag.StringVar(&deleteSpeakerID, "delete", "", "删除指定说话人ID的声纹")
	flag.StringVar(&customSpeakerID, "speaker-id", speakerID, "说话人ID（默认：test_speaker_001）")
	flag.StringVar(&customSpeakerName, "speaker-name", speakerName, "说话人名称（默认：测试说话人）")
	flag.StringVar(&customUID, "uid", defaultUID, "用户ID（默认：test_user_001）")
	flag.StringVar(&customAgentID, "agent-id", defaultAgentID, "代理ID（默认：test_agent_001，服务端必填）")
	flag.StringVar(&customUUID, "uuid", defaultUUID, "注册会话UUID（默认：test_uuid_001，服务端必填）")
	flag.IntVar(&maxFrames, "frames", 0, "流式识别时发送的帧数（0表示发送所有帧，可选）")
	flag.Float64Var(&threshold, "threshold", 0, "识别阈值（>0时使用，否则使用服务端默认值，可选）")
	flag.IntVar(&peekStartMs, "peek-start-ms", 1200, "流式识别中首次发送peek的累计音频时长（毫秒，<=0表示不发送peek）")
	flag.IntVar(&peekIntervalMs, "peek-interval-ms", 200, "流式识别中peek发送间隔（毫秒，<=0表示只发送一次peek）")
	flag.StringVar(&customBaseURL, "base-url", baseURL, "服务HTTP地址（例如：http://127.0.0.1:9000）")
	flag.StringVar(&customSpeakerWSURL, "speaker-ws-url", "", "声纹WS地址（留空则按base-url自动推导）")
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
	fmt.Println("声纹识别测试程序")
	fmt.Println("========================================")
	fmt.Printf("服务地址: %s\n", baseURL)
	fmt.Printf("声纹WS地址: %s\n", speakerWSURL)

	// Nếu tất cả các tham số không được chỉ định, hướng dẫn hiển thị
	if registerFile == "" && identifyFile == "" && !listSpeakers && deleteSpeakerID == "" {
		fmt.Println("\n使用方法:")
		fmt.Println("  go run test_speaker.go -register <注册文件>")
		fmt.Println("  go run test_speaker.go -identify <识别文件>")
		fmt.Println("  go run test_speaker.go -list")
		fmt.Println("  go run test_speaker.go -delete <说话人ID>")
		fmt.Println("  go run test_speaker.go -register <注册文件> -identify <识别文件>")
		fmt.Println("\n参数说明:")
		fmt.Println("  -register <文件路径>    注册声纹的音频文件（WAV格式，可选）")
		fmt.Println("  -identify <文件路径>    识别声纹的音频文件（WAV格式，可选）")
		fmt.Println("  -list                   列出所有已注册的声纹（可选）")
		fmt.Println("  -delete <说话人ID>       删除指定说话人ID的声纹（可选）")
		fmt.Println("  -speaker-id <ID>        说话人ID（可选，默认：test_speaker_001）")
		fmt.Println("  -speaker-name <名称>    说话人名称（可选，默认：测试说话人）")
		fmt.Println("  -uid <用户ID>           用户ID（可选，默认：test_user_001）")
		fmt.Println("  -agent-id <代理ID>      代理ID（可选，默认：test_agent_001）")
		fmt.Println("  -uuid <UUID>            注册会话UUID（可选，默认：test_uuid_001，注册时服务端必填）")
		fmt.Println("  -frames <帧数>         流式识别时发送的帧数（0表示发送所有帧，可选）")
		fmt.Println("  -threshold <阈值>      识别阈值（>0时使用，否则使用服务端默认值，可选）")
		fmt.Println("  -peek-start-ms <毫秒>   流式识别首次peek时间点（<=0不发送peek）")
		fmt.Println("  -peek-interval-ms <毫秒> peek发送间隔（<=0只发送一次peek）")
		fmt.Println("  -base-url <URL>        服务HTTP地址（默认：http://127.0.0.1:9000）")
		fmt.Println("  -speaker-ws-url <URL>  声纹WS地址（默认按base-url自动推导）")
		fmt.Println("\n示例:")
		fmt.Println("  # 仅注册声纹")
		fmt.Println("  go run test_speaker.go -register register.wav")
		fmt.Println("  # 仅识别声纹")
		fmt.Println("  go run test_speaker.go -identify identify.wav")
		fmt.Println("  # 列出所有声纹")
		fmt.Println("  go run test_speaker.go -list")
		fmt.Println("  # 删除声纹")
		fmt.Println("  go run test_speaker.go -delete test_speaker_001")
		fmt.Println("  # 注册并识别")
		fmt.Println("  go run test_speaker.go -register register.wav -identify identify.wav")
		fmt.Println("  go run test_speaker.go -register test.wav -identify test.wav -speaker-id user001 -uid user001")
		fmt.Println("  # 流式识别时只发送前10帧")
		fmt.Println("  go run test_speaker.go -identify test.wav -frames 10")
		fmt.Println("  # 使用自定义阈值识别")
		fmt.Println("  go run test_speaker.go -identify test.wav -threshold 0.7")
		fmt.Println("  # 流式识别中获取中间结果（peek）")
		fmt.Println("  go run test_speaker.go -identify test.wav -peek-start-ms 1200 -peek-interval-ms 200")
		fmt.Println("  # 指定测试服务地址")
		fmt.Println("  go run test_speaker.go -identify test.wav -base-url http://127.0.0.1:9000")
		os.Exit(1)
	}

	// Xử lý truy vấn danh sách
	if listSpeakers {
		fmt.Printf("\n步骤 1: 获取声纹列表 (用户ID: %s", customUID)
		if customAgentID != "" {
			fmt.Printf(", 代理ID: %s", customAgentID)
		}
		fmt.Println(")...")
		if err := listSpeakersFunc(customUID, customAgentID); err != nil {
			fmt.Printf("❌ 获取列表失败: %v\n", err)
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
		fmt.Printf("\n步骤 %d: 删除声纹 (说话人ID: %s, 用户ID: %s", stepNum, deleteSpeakerID, customUID)
		if customAgentID != "" {
			fmt.Printf(", 代理ID: %s", customAgentID)
		}
		fmt.Println(")...")
		if err := deleteSpeaker(deleteSpeakerID, customUID, customAgentID); err != nil {
			fmt.Printf("❌ 删除失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ 删除成功")
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
			fmt.Printf("❌ 错误: 无法解析注册文件路径: %v\n", err)
			os.Exit(1)
		}
		if _, err := os.Stat(registerPath); os.IsNotExist(err) {
			fmt.Printf("❌ 错误: 找不到注册文件 %s\n", registerPath)
			os.Exit(1)
		}
		fmt.Printf("✅ 找到注册音频文件: %s\n", registerPath)

		// Tính số bước
		stepNum := 1
		if listSpeakers {
			stepNum++
		}
		if deleteSpeakerID != "" {
			stepNum++
		}

		// Đăng ký giọng nói
		fmt.Printf("\n步骤 %d: 注册声纹 (使用文件: %s)...\n", stepNum, filepath.Base(registerPath))
		if err := registerSpeaker(registerPath, customSpeakerID, customSpeakerName, customUID, customAgentID, customUUID); err != nil {
			fmt.Printf("❌ 注册失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ 注册成功")

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
			fmt.Printf("❌ 错误: 无法解析识别文件路径: %v\n", err)
			os.Exit(1)
		}
		if _, err := os.Stat(identifyPath); os.IsNotExist(err) {
			fmt.Printf("❌ 错误: 找不到识别文件 %s\n", identifyPath)
			os.Exit(1)
		}
		fmt.Printf("✅ 找到识别音频文件: %s\n", identifyPath)

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
		fmt.Printf("\n步骤 %d: HTTP 识别声纹 (使用文件: %s", stepNum, filepath.Base(identifyPath))
		if threshold > 0 {
			fmt.Printf(", 阈值: %.4f", threshold)
		}
		fmt.Println(")...")
		result, err := identifySpeaker(identifyPath, customUID, customAgentID, threshold)
		if err != nil {
			fmt.Printf("❌ 识别失败: %v\n", err)
			os.Exit(1)
		}

		// Hiển thị kết quả nhận dạng HTTP
		fmt.Println("\nHTTP 识别结果:")
		fmt.Println("========================================")
		fmt.Printf("识别状态: %v\n", result.Identified)
		if result.Identified {
			fmt.Printf("说话人ID: %s\n", result.SpeakerID)
			fmt.Printf("说话人名称: %s\n", result.SpeakerName)
			fmt.Printf("相似度: %.4f\n", result.Confidence)
			fmt.Printf("阈值: %.4f\n", result.Threshold)
			if result.Confidence >= result.Threshold {
				fmt.Println("✅ 识别成功，相似度超过阈值")
			} else {
				fmt.Println("⚠️  识别成功，但相似度低于阈值")
			}
		} else {
			fmt.Println("❌ 未识别到匹配的说话人")
		}
		fmt.Println("========================================")

		// Nhận dạng phát trực tuyến WebSocket
		stepNum++
		fmt.Printf("\n步骤 %d: WebSocket 流式识别 (使用文件: %s", stepNum, filepath.Base(identifyPath))
		if maxFrames > 0 {
			fmt.Printf(", 发送前 %d 帧", maxFrames)
		}
		if threshold > 0 {
			fmt.Printf(", 阈值: %.4f", threshold)
		}
		if peekStartMs > 0 {
			fmt.Printf(", peek起始: %dms, peek间隔: %dms", peekStartMs, peekIntervalMs)
		} else {
			fmt.Printf(", peek: 关闭")
		}
		fmt.Println(")...")
		wsResult, err := identifySpeakerWebSocket(identifyPath, customUID, customAgentID, maxFrames, threshold, peekStartMs, peekIntervalMs)
		if err != nil {
			fmt.Printf("❌ WebSocket 识别失败: %v\n", err)
			os.Exit(1)
		}

		// Hiển thị kết quả nhận dạng WebSocket
		fmt.Println("\nWebSocket 流式识别结果:")
		fmt.Println("========================================")
		fmt.Printf("识别状态: %v\n", wsResult.Identified)
		if wsResult.Identified {
			fmt.Printf("说话人ID: %s\n", wsResult.SpeakerID)
			fmt.Printf("说话人名称: %s\n", wsResult.SpeakerName)
			fmt.Printf("相似度: %.4f\n", wsResult.Confidence)
			fmt.Printf("阈值: %.4f\n", wsResult.Threshold)
			if wsResult.Confidence >= wsResult.Threshold {
				fmt.Println("✅ 识别成功，相似度超过阈值")
			} else {
				fmt.Println("⚠️  识别成功，但相似度低于阈值")
			}
		} else {
			fmt.Println("❌ 未识别到匹配的说话人")
		}
		fmt.Println("========================================")
	}
}

// registerLoa đăng ký giọng nói
func registerSpeaker(wavPath string, sid string, sname string, uid string, agentID string, uuid string) error {
	// mở tập tin
	file, err := os.Open(wavPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %v", err)
	}
	defer file.Close()

	// Tạo trình soạn thảo nhiều phần
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Thêm trường biểu mẫu
	if err := writer.WriteField("uid", uid); err != nil {
		return fmt.Errorf("写入 uid 失败: %v", err)
	}

	if agentID != "" {
		if err := writer.WriteField("agent_id", agentID); err != nil {
			return fmt.Errorf("写入 agent_id 失败: %v", err)
		}
	}

	if uuid != "" {
		if err := writer.WriteField("uuid", uuid); err != nil {
			return fmt.Errorf("写入 uuid 失败: %v", err)
		}
	}

	if err := writer.WriteField("speaker_id", sid); err != nil {
		return fmt.Errorf("写入 speaker_id 失败: %v", err)
	}

	if err := writer.WriteField("speaker_name", sname); err != nil {
		return fmt.Errorf("写入 speaker_name 失败: %v", err)
	}

	// Thêm tập tin
	part, err := writer.CreateFormFile("audio", filepath.Base(wavPath))
	if err != nil {
		return fmt.Errorf("创建文件字段失败: %v", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("复制文件内容失败: %v", err)
	}

	// Đóng nhà văn
	if err := writer.Close(); err != nil {
		return fmt.Errorf("关闭 writer 失败: %v", err)
	}

	// Tạo yêu cầu HTTP
	req, err := http.NewRequest("POST", speakerAPI+"/register", &requestBody)
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
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
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// Đọc phản hồi
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %v", err)
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
		return fmt.Errorf("解析响应失败: %v", err)
	}

	fmt.Printf("   用户ID: %s\n", uid)
	fmt.Printf("   注册ID: %s\n", registerResp.SpeakerID)
	fmt.Printf("   注册名称: %s\n", registerResp.SpeakerName)

	return nil
}

// nhận dạngSpeaker nhận dạng giọng nói
// ngưỡng: ngưỡng nhận dạng, nếu <= 0 thì sử dụng giá trị mặc định của máy chủ
func identifySpeaker(wavPath string, uid string, agentID string, threshold float64) (*IdentifyResult, error) {
	// mở tập tin
	file, err := os.Open(wavPath)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %v", err)
	}
	defer file.Close()

	// Tạo trình soạn thảo nhiều phần
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Thêm uid trường biểu mẫu
	if err := writer.WriteField("uid", uid); err != nil {
		return nil, fmt.Errorf("写入 uid 失败: %v", err)
	}

	// Thêm trường biểu mẫu Agent_id (nếu được cung cấp)
	if agentID != "" {
		if err := writer.WriteField("agent_id", agentID); err != nil {
			return nil, fmt.Errorf("写入 agent_id 失败: %v", err)
		}
	}

	// Thêm ngưỡng trường biểu mẫu (nếu được cung cấp và > 0)
	if threshold > 0 {
		if err := writer.WriteField("threshold", fmt.Sprintf("%.6f", threshold)); err != nil {
			return nil, fmt.Errorf("写入 threshold 失败: %v", err)
		}
	}

	// Thêm tập tin
	part, err := writer.CreateFormFile("audio", filepath.Base(wavPath))
	if err != nil {
		return nil, fmt.Errorf("创建文件字段失败: %v", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("复制文件内容失败: %v", err)
	}

	// Đóng nhà văn
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("关闭 writer 失败: %v", err)
	}

	// Tạo yêu cầu HTTP
	req, err := http.NewRequest("POST", speakerAPI+"/identify", &requestBody)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
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
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// Đọc phản hồi
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
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
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}

	return &result, nil
}

// readWavToFloat32 đọc các tệp WAV và chuyển đổi chúng thành mảng float32
func readWavToFloat32(wavPath string) ([]float32, int, error) {
	// mở tập tin
	file, err := os.Open(wavPath)
	if err != nil {
		return nil, 0, fmt.Errorf("打开文件失败: %v", err)
	}
	defer file.Close()

	// Tạo bộ giải mã WAV
	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return nil, 0, fmt.Errorf("无效的WAV文件")
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
			return nil, 0, fmt.Errorf("读取WAV数据失败: %v", err)
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
		return nil, fmt.Errorf("读取音频文件失败: %v", err)
	}

	fmt.Printf("   音频采样率: %d Hz\n", sampleRate)
	fmt.Printf("   音频样本数: %d\n", len(audioData))
	fmt.Printf("   音频时长: %.2f 秒\n", float64(len(audioData))/float64(sampleRate))
	fmt.Printf("   注意: 客户端不进行重采样，服务端将自动重采样到模型期望的采样率\n")

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
		return nil, fmt.Errorf("WebSocket连接失败: %v", err)
	}
	defer conn.Close()

	// Đặt thời gian chờ đọc
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Nhận tin nhắn xác nhận kết nối
	var connectionMsg map[string]interface{}
	if err := conn.ReadJSON(&connectionMsg); err != nil {
		return nil, fmt.Errorf("读取连接确认消息失败: %v", err)
	}
	if msgType, ok := connectionMsg["type"].(string); !ok || msgType != "connection" {
		return nil, fmt.Errorf("意外的连接消息: %v", connectionMsg)
	}
	fmt.Printf("   ✅ WebSocket连接成功\n")

	// Gửi dữ liệu âm thanh theo từng đoạn (~20ms mỗi đoạn)
	chunkSize := sampleRate * 20 / 1000 // Số lượng mẫu trong 20ms
	totalChunks := (len(audioData) + chunkSize - 1) / chunkSize

	// Nếu số lượng khung tối đa được chỉ định, hãy giới hạn số lượng khối được gửi
	chunksToSend := totalChunks
	if maxFrames > 0 && maxFrames < totalChunks {
		chunksToSend = maxFrames
		fmt.Printf("   开始发送音频数据（发送前 %d/%d 块，每块约 %d 样本）...\n", chunksToSend, totalChunks, chunkSize)
	} else {
		fmt.Printf("   开始发送音频数据（分 %d 块，每块约 %d 样本）...\n", totalChunks, chunkSize)
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
		fmt.Printf("   🔎 已启用peek: start=%dms, interval=%dms\n", peekStartMs, peekIntervalMs)
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
					errorChan <- fmt.Errorf("WebSocket读取错误: %v", err)
				}
				return
			}

			if messageType == websocket.TextMessage {
				var msg map[string]interface{}
				if err := json.Unmarshal(message, &msg); err != nil {
					fmt.Printf("   ⚠️  无法解析消息: %v\n", err)
					continue
				}

				if msgType, ok := msg["type"].(string); ok {
					switch msgType {
					case "audio_received":
						// Xác nhận tiếp nhận âm thanh
						if samples, ok := msg["samples"].(float64); ok {
							fmt.Printf("   📦 服务器确认收到 %d 样本\n", int(samples))
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
							fmt.Printf("   ⏱️  peek(%s) 被限流，round=%d, audio=%.0fms\n", requestID, round, audioMs)
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
								fmt.Printf("   🔍 peek(%s): round=%d, audio=%.0fms, 暂未识别到匹配说话人\n", requestID, round, audioMs)
							}
						} else if errMsg, ok := msg["error"].(string); ok {
							fmt.Printf("   ⚠️  peek(%s) 返回错误: %s\n", requestID, errMsg)
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
							errorChan <- fmt.Errorf("服务器错误: %s", errMsg)
							return
						}
					default:
						fmt.Printf("   ⚠️  收到未知消息类型: %s, 内容: %v\n", msgType, msg)
					}
				} else {
					fmt.Printf("   ⚠️  消息格式异常: %v\n", msg)
				}
			} else {
				fmt.Printf("   ⚠️  收到非文本消息，类型: %d\n", messageType)
			}
		}
	}()

	// Gửi khối dữ liệu âm thanh
	totalSamplesSent := 0
	currentChunk := 0
	for i := 0; i < len(audioData); i += chunkSize {
		// Nếu chỉ định số lượng khung tối đa, hãy kiểm tra xem đã đạt đến giới hạn chưa (kiểm tra trước khi gửi)
		if maxFrames > 0 && currentChunk >= maxFrames {
			fmt.Printf("   ⚠️  已达到最大帧数限制 (%d 帧)，停止发送\n", maxFrames)
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
			return nil, fmt.Errorf("发送音频数据失败: %v", err)
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
				return nil, fmt.Errorf("发送peek命令失败: %v", err)
			}
			fmt.Printf("   🔎 已发送peek请求 %s (累计音频 %.0fms)\n",
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
			fmt.Printf("   已发送 %d/%d 块 (共 %d 样本)\n", currentChunk, chunksToSend, totalSamplesSent)
		}
	}

	if totalSamplesSent != len(audioData) {
		fmt.Printf("   ⚠️  警告: 发送的样本数 (%d) 与总样本数 (%d) 不匹配\n", totalSamplesSent, len(audioData))
	}

	fmt.Printf("   ✅ 音频数据发送完成\n")

	// Gửi lệnh hoàn thành
	finishCmd := map[string]interface{}{
		"action": "finish",
	}
	if err := conn.WriteJSON(finishCmd); err != nil {
		return nil, fmt.Errorf("发送完成命令失败: %v", err)
	}
	fmt.Printf("   ✅ 已发送完成命令，等待识别结果...\n")

	// Đang chờ kết quả
	select {
	case result := <-resultChan:
		// Hiển thị chi tiết nhận dạng
		if !result.Identified {
			fmt.Printf("   ⚠️  识别失败: 相似度 %.4f < 阈值 %.4f\n", result.Confidence, result.Threshold)
		}
		return result, nil
	case err := <-errorChan:
		return nil, err
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("等待识别结果超时（15秒）")
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
		return fmt.Errorf("解析URL失败: %v", err)
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
		return fmt.Errorf("创建请求失败: %v", err)
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
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// Đọc phản hồi
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %v", err)
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
		return fmt.Errorf("解析响应失败: %v", err)
	}

	// Hiển thị kết quả
	fmt.Println("\n声纹列表:")
	fmt.Println("========================================")
	fmt.Printf("用户ID: %s\n", listResp.UID)
	fmt.Printf("总数: %d\n", listResp.Total)

	if len(listResp.Speakers) == 0 {
		fmt.Println("\n暂无已注册的声纹")
	} else {
		fmt.Println("\n说话人列表:")
		fmt.Println("----------------------------------------")
		for i, speaker := range listResp.Speakers {
			fmt.Printf("%d. 说话人ID: %s\n", i+1, speaker.ID)
			fmt.Printf("   说话人名称: %s\n", speaker.Name)
			fmt.Printf("   样本数量: %d\n", speaker.SampleCount)
			fmt.Printf("   创建时间: %s\n", speaker.CreatedAt)
			fmt.Printf("   更新时间: %s\n", speaker.UpdatedAt)
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
		return fmt.Errorf("解析URL失败: %v", err)
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
		return fmt.Errorf("创建请求失败: %v", err)
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
		return fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()

	// Đọc phản hồi
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %v", err)
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
		return fmt.Errorf("解析响应失败: %v", err)
	}

	fmt.Printf("   用户ID: %s\n", deleteResp.UID)
	fmt.Printf("   说话人ID: %s\n", deleteResp.SpeakerID)
	fmt.Printf("   消息: %s\n", deleteResp.Message)

	return nil
}
