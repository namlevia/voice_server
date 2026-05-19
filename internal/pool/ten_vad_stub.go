//go:build linux && !amd64

package pool

import (
	"errors"
	"unsafe"
)

type TenVADDLL struct{}

func GetInstance() *TenVADDLL {
	return &TenVADDLL{}
}

func (t *TenVADDLL) CreateInstance(hopSize int, threshold float32) (unsafe.Pointer, error) {
	return nil, errors.New("TEN-VAD is not available on this platform")
}

func (t *TenVADDLL) ProcessAudio(handle unsafe.Pointer, audioData []int16) (float32, int32, error) {
	return 0, 0, errors.New("TEN-VAD is not available on this platform")
}

func (t *TenVADDLL) DestroyInstance(handle unsafe.Pointer) error {
	return nil
}

func (t *TenVADDLL) GetVersion() string {
	return "unavailable"
}
