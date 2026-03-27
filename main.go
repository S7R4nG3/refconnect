package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/S7R4nG3/refconnect/internal/config"
	"github.com/S7R4nG3/refconnect/internal/ui"
)

func main() {
	// Redirect all log output to a file so the terminal stays silent.
	if exe, err := os.Executable(); err == nil {
		logPath := filepath.Join(filepath.Dir(exe), "refconnect.log")
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			log.SetOutput(f)
		}
	}


	cfg, err := config.Load()
	if err != nil {
		log.Printf("config load error (using defaults): %v", err)
		cfg = config.Default()
	}

	ui.Run(cfg)
}
