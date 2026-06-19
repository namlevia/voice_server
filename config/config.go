package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Cấu hình cấu hình cấu hình
type Config struct {
	Server struct {
		Port           int    `mapstructure:"port"`
		Host           string `mapstructure:"host"`
		MaxConnections int    `mapstructure:"max_connections"`
		ReadTimeout    int    `mapstructure:"read_timeout"`
		WebSocket      struct {
			ReadTimeout       int  `mapstructure:"read_timeout"`
			MaxMessageSize    int  `mapstructure:"max_message_size"`
			ReadBufferSize    int  `mapstructure:"read_buffer_size"`
			WriteBufferSize   int  `mapstructure:"write_buffer_size"`
			EnableCompression bool `mapstructure:"enable_compression"`
		} `mapstructure:"websocket"`
	} `mapstructure:"server"`
	Session struct {
		SendQueueSize int `mapstructure:"send_queue_size"`
		MaxSendErrors int `mapstructure:"max_send_errors"`
	} `mapstructure:"session"`
	VAD         VADConfig `mapstructure:"vad"`
	Recognition struct {
		Enabled                     bool   `mapstructure:"enabled"`
		ModelType                   string `mapstructure:"model_type"`
		ModelPath                   string `mapstructure:"model_path"`
		EncoderPath                 string `mapstructure:"encoder_path"`
		DecoderPath                 string `mapstructure:"decoder_path"`
		JoinerPath                  string `mapstructure:"joiner_path"`
		TokensPath                  string `mapstructure:"tokens_path"`
		Language                    string `mapstructure:"language"`
		UseInverseTextNormalization bool   `mapstructure:"use_inverse_text_normalization"`
		NumThreads                  int    `mapstructure:"num_threads"`
		Provider                    string `mapstructure:"provider"`
		Debug                       bool   `mapstructure:"debug"`
	} `mapstructure:"recognition"`
	Speaker struct {
		Enabled          bool    `mapstructure:"enabled"`
		ModelPath        string  `mapstructure:"model_path"`
		NumThreads       int     `mapstructure:"num_threads"`
		Provider         string  `mapstructure:"provider"`
		Threshold        float32 `mapstructure:"threshold"`
		DataDir          string  `mapstructure:"data_dir"`
		SaveAudioOnFinish bool   `mapstructure:"save_audio_on_finish"`
		AudioSaveDir     string  `mapstructure:"audio_save_dir"`
		StorageType      string  `mapstructure:"storage_type"`
		JSONStorage      struct {
			FilePath string `mapstructure:"file_path"`
		} `mapstructure:"json_storage"`
		Qdrant struct {
			Host           string `mapstructure:"host"`
			Port           int    `mapstructure:"port"`
			CollectionName string `mapstructure:"collection_name"`
		} `mapstructure:"qdrant"`
	} `mapstructure:"speaker"`
	Audio struct {
		SampleRate      int     `mapstructure:"sample_rate"`
		FeatureDim      int     `mapstructure:"feature_dim"`
		NormalizeFactor float32 `mapstructure:"normalize_factor"`
		ChunkSize       int     `mapstructure:"chunk_size"`
	} `mapstructure:"audio"`
	Pool struct {
		InstanceMode string `mapstructure:"instance_mode"`
		WorkerCount  int    `mapstructure:"worker_count"`
		QueueSize    int    `mapstructure:"queue_size"`
	} `mapstructure:"pool"`
	RateLimit struct {
		Enabled           bool `mapstructure:"enabled"`
		RequestsPerSecond int  `mapstructure:"requests_per_second"`
		BurstSize         int  `mapstructure:"burst_size"`
		MaxConnections    int  `mapstructure:"max_connections"`
	} `mapstructure:"rate_limit"`
	Response struct {
		SendMode string `mapstructure:"send_mode"`
		Timeout  int    `mapstructure:"timeout"`
	} `mapstructure:"response"`
	Logging struct {
		Level      string `mapstructure:"level"`
		Format     string `mapstructure:"format"`
		Output     string `mapstructure:"output"`
		FilePath   string `mapstructure:"file_path"`
		MaxSize    int    `mapstructure:"max_size"`
		MaxBackups int    `mapstructure:"max_backups"`
		MaxAge     int    `mapstructure:"max_age"`
		Compress   bool   `mapstructure:"compress"`
	} `mapstructure:"logging"`
}

type VADConfig struct {
	Provider  string        `mapstructure:"provider"`
	PoolSize  int           `mapstructure:"pool_size"`
	Threshold float32       `mapstructure:"threshold"`
	SileroVAD SileroVADConf `mapstructure:"silero_vad"`
	TenVAD    TenVADConf    `mapstructure:"ten_vad"`
}

type SileroVADConf struct {
	ModelPath          string  `mapstructure:"model_path"`
	Threshold          float32 `mapstructure:"threshold"`
	MinSilenceDuration float32 `mapstructure:"min_silence_duration"`
	MinSpeechDuration  float32 `mapstructure:"min_speech_duration"`
	MaxSpeechDuration  float32 `mapstructure:"max_speech_duration"`
	WindowSize         int     `mapstructure:"window_size"`
	BufferSizeSeconds  float32 `mapstructure:"buffer_size_seconds"`
}

type TenVADConf struct {
	HopSize          int `mapstructure:"hop_size"`
	MinSpeechFrames  int `mapstructure:"min_speech_frames"`
	MaxSilenceFrames int `mapstructure:"max_silence_frames"`
}

var GlobalConfig Config

// Cấu hình khởi tạo initConfig
func InitConfig(configPath string) error {
	// Đặt tên và đường dẫn tệp cấu hình
	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("json")
		viper.AddConfigPath(".")
		viper.AddConfigPath("./config")
		viper.AddConfigPath("/etc/voice_server/")
	}

	// Đặt tiền tố biến môi trường
	viper.SetEnvPrefix("VAD_ASR")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Đọc tập tin cấu hình
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Không tìm thấy tệp cấu hình, sử dụng giá trị mặc định
			fmt.Println("⚠️  Config file not found, using defaults")
		} else {
			// Đã tìm thấy tệp cấu hình nhưng đọc lỗi
			return fmt.Errorf("error reading config file: %w", err)
		}
	} else {
		fmt.Printf("✅ Using config file: %s\n", viper.ConfigFileUsed())
	}

	// Phân tích cấu hình thành cấu trúc
	if err := viper.Unmarshal(&GlobalConfig); err != nil {
		return fmt.Errorf("error unmarshaling config: %w", err)
	}

	return nil
}

// LoadConfig tải tệp cấu hình (duy trì khả năng tương thích ngược)
func LoadConfig(filename string) error {
	return InitConfig(filename)
}

// GetConfig Nhận cấu hình
func GetConfig() *Config {
	return &GlobalConfig
}

// GetViper có phiên bản viper
func GetViper() *viper.Viper {
	return viper.GetViper()
}

// WatchConfig giám sát các thay đổi của tệp cấu hình (lỗi thời, sử dụng HotReloadManager)
func WatchConfig(callback func()) {
	fmt.Println("⚠️  WatchConfig is deprecated, use HotReloadManager instead")
}

// SaveConfig lưu cấu hình vào một tập tin
func SaveConfig() error {
	return viper.WriteConfig()
}

// SaveConfigAs lưu cấu hình vào tệp được chỉ định
func SaveConfigAs(filename string) error {
	return viper.WriteConfigAs(filename)
}

// SetConfigValue đặt giá trị cấu hình
func SetConfigValue(key string, value interface{}) {
	viper.Set(key, value)
	// Phân tích lại cấu trúc
	viper.Unmarshal(&GlobalConfig)
}

// GetConfigValue Lấy giá trị cấu hình
func GetConfigValue(key string) interface{} {
	return viper.Get(key)
}

// GetString Lấy giá trị cấu hình chuỗi
func GetString(key string) string {
	return viper.GetString(key)
}

// GetInt nhận giá trị cấu hình số nguyên
func GetInt(key string) int {
	return viper.GetInt(key)
}

// GetBool nhận giá trị cấu hình Boolean
func GetBool(key string) bool {
	return viper.GetBool(key)
}

// GetFloat64 Lấy giá trị cấu hình dấu phẩy động
func GetFloat64(key string) float64 {
	return viper.GetFloat64(key)
}

// PrintConfig in cấu hình hiện tại
func PrintConfig() {
	fmt.Println("📋 Current Configuration:")
	fmt.Printf("  Server: %s:%d\n", GlobalConfig.Server.Host, GlobalConfig.Server.Port)
	fmt.Printf("  VAD Model: %s\n", GlobalConfig.VAD.SileroVAD.ModelPath)
	fmt.Printf("  ASR Model: %s\n", GlobalConfig.Recognition.ModelPath)
	fmt.Printf("  Pool Workers: %d\n", GlobalConfig.Pool.WorkerCount)
	fmt.Printf("  VAD Pool Size: %d\n", GlobalConfig.VAD.PoolSize)
	fmt.Printf("  Log Level: %s\n", GlobalConfig.Logging.Level)
	fmt.Printf("  Log Output: %s\n", GlobalConfig.Logging.FilePath)
}
