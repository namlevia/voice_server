package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-audio/wav"
	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"voice_server/internal/bootstrap"
)

func TranscribeHandler(deps *bootstrap.AppDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		file, err := c.FormFile("audio")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Vui long cung cap file audio"})
			return
		}

		f, err := file.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Mo file that bai"})
			return
		}
		defer f.Close()

		decoder := wav.NewDecoder(f)
		buf, err := decoder.FullPCMBuffer()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Giai ma file wav that bai"})
			return
		}

		// Convert int to float32
		pcmData := make([]float32, len(buf.Data))
		for i, v := range buf.Data {
			pcmData[i] = float32(v) / 32768.0
		}

		if deps.GlobalRecognizer == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Recognizer chua san sang"})
			return
		}

		stream := sherpa.NewOfflineStream(deps.GlobalRecognizer)
		defer sherpa.DeleteOfflineStream(stream)

		stream.AcceptWaveform(16000, pcmData)
		deps.GlobalRecognizer.Decode(stream)
		
		result := stream.GetResult()
		text := ""
		if result != nil {
			text = result.Text
		}

		c.JSON(http.StatusOK, gin.H{
			"text":          text,
			"transcription": text,
			"result":        text,
		})
	}
}

