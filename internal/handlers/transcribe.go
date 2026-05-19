package handlers

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"voice_server/config"
	"voice_server/internal/bootstrap"

	"github.com/gin-gonic/gin"
	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

func TranscribeHandler(deps *bootstrap.AppDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.GlobalRecognizer == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "recognizer is not initialized"})
			return
		}

		file, err := c.FormFile("audio")
		if err != nil {
			file, err = c.FormFile("file")
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "audio field is required"})
			return
		}

		src, err := file.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("open audio failed: %v", err)})
			return
		}
		defer src.Close()

		data, err := io.ReadAll(src)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("read audio failed: %v", err)})
			return
		}

		samples, sampleRate, err := decodeWAVToFloat32(bytes.NewReader(data))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if len(samples) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "audio is empty"})
			return
		}
		if rawSampleRate := c.PostForm("sample_rate"); rawSampleRate != "" {
			if parsed, parseErr := strconv.Atoi(rawSampleRate); parseErr == nil && parsed > 0 {
				sampleRate = parsed
			}
		}

		startedAt := time.Now()
		stream := sherpa.NewOfflineStream(deps.GlobalRecognizer)
		defer sherpa.DeleteOfflineStream(stream)
		stream.AcceptWaveform(sampleRate, samples)
		deps.GlobalRecognizer.Decode(stream)
		result := stream.GetResult()
		text := ""
		if result != nil {
			text = result.Text
		}

		c.JSON(http.StatusOK, gin.H{
			"text":        text,
			"language":    "vi",
			"provider":    "sherpa_vietnamese_asr_go",
			"duration_ms": time.Since(startedAt).Milliseconds(),
		})
	}
}

func decodeWAVToFloat32(r io.ReadSeeker) ([]float32, int, error) {
	dec := wav.NewDecoder(r)
	if !dec.IsValidFile() {
		return nil, 0, fmt.Errorf("invalid WAV file")
	}
	dec.ReadInfo()
	format := dec.Format()
	if format == nil || format.SampleRate <= 0 {
		return nil, 0, fmt.Errorf("invalid WAV format")
	}

	channels := format.NumChannels
	if channels <= 0 {
		channels = 1
	}
	frameSize := format.SampleRate * channels
	if frameSize <= 0 {
		frameSize = config.GlobalConfig.Audio.SampleRate * channels
	}
	buf := &audio.IntBuffer{Format: format, SourceBitDepth: int(dec.BitDepth), Data: make([]int, frameSize)}
	var out []float32
	for {
		n, err := dec.PCMBuffer(buf)
		if err == io.EOF || n == 0 {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("decode WAV failed: %w", err)
		}
		data := buf.Data[:n]
		if channels == 1 {
			for _, sample := range data {
				out = append(out, normalizePCM(sample, int(dec.BitDepth)))
			}
			continue
		}
		for i := 0; i+channels <= len(data); i += channels {
			var sum float32
			for ch := 0; ch < channels; ch++ {
				sum += normalizePCM(data[i+ch], int(dec.BitDepth))
			}
			out = append(out, sum/float32(channels))
		}
	}
	return out, format.SampleRate, nil
}

func normalizePCM(sample int, bitDepth int) float32 {
	if bitDepth <= 0 {
		bitDepth = 16
	}
	maxValue := float32(int64(1) << (bitDepth - 1))
	return float32(sample) / maxValue
}
