//go:build android

package pool

import (
	"errors"
	"sync"
	"unsafe"
)

// TenVADDLL - stub không hỗ trợ CGO trên Android (ten-vad chưa có thư viện Android)
type TenVADDLL struct{}

var (
	globalTenVAD *TenVADDLL
	once         sync.Once
)

// GetInstance trả về singleton TEN-VAD (stub trên Android)
func GetInstance() *TenVADDLL {
	once.Do(func() {
		globalTenVAD = &TenVADDLL{}
	})
	return globalTenVAD
}

// CreateInstance - không hỗ trợ trên Android
func (t *TenVADDLL) CreateInstance(hopSize int, threshold float32) (unsafe.Pointer, error) {
	return nil, errors.New("ten-vad không được hỗ trợ trên Android, hãy dùng silero_vad")
}

// ProcessAudio - không hỗ trợ trên Android
func (t *TenVADDLL) ProcessAudio(handle unsafe.Pointer, audioData []int16) (float32, int32, error) {
	return 0, 0, errors.New("ten-vad không được hỗ trợ trên Android, hãy dùng silero_vad")
}

// DestroyInstance - không hỗ trợ trên Android
func (t *TenVADDLL) DestroyInstance(handle unsafe.Pointer) error {
	return errors.New("ten-vad không được hỗ trợ trên Android, hãy dùng silero_vad")
}

// GetVersion - trả về chuỗi báo không hỗ trợ
func (t *TenVADDLL) GetVersion() string {
	return "ten-vad not supported on android"
}
