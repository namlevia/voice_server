package main

import (
	"fmt"
	"voice_server/config"
	"voice_server/internal/bootstrap"
)

func main() {
	if err := config.InitConfig("c:\\Users\\CAD-4M\\Documents\\LeviaTech-Xiaozhi-Server-Vi-Lite-main-test\\asr_server.json"); err != nil {
		fmt.Printf("Error InitConfig: %v\n", err)
		return
	}
	cfg := config.GetConfig()
	fmt.Printf("ModelType: '%s'\n", cfg.Recognition.ModelType)
	fmt.Printf("Encoder: '%s'\n", cfg.Recognition.EncoderPath)
	fmt.Printf("Tokens: '%s'\n", cfg.Recognition.TokensPath)

	_, err := bootstrap.InitDependencies(cfg)
	if err != nil {
		fmt.Printf("Error InitDependencies: %v\n", err)
	} else {
		fmt.Println("SUCCESS: InitDependencies")
	}
}
