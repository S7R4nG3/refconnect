package main

import (
	"log"
	"os"
	"time"

	"github.com/S7R4nG3/refconnect/internal/config"
	"github.com/S7R4nG3/refconnect/internal/ui"
)

// pruneEmptyLogs removes zero-byte .log files from dir.
func pruneEmptyLogs(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && info.Size() == 0 {
			os.Remove(dir + "/" + e.Name()) //nolint:errcheck
		}
	}
}

func main() {
	// Open a timestamped log file in the user's config Logs/ directory.
	// Falls back to stderr on any error so logging is never silently lost.
	if logDir, err := config.LogDir(); err == nil {
		if err := os.MkdirAll(logDir, 0o755); err == nil {
			pruneEmptyLogs(logDir)
			name := time.Now().Format("2006-01-02_15-04-05") + ".log"
			if f, err := os.OpenFile(logDir+"/"+name, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				defer f.Close()
				log.SetOutput(f)
			}
		}
	}

	cfg, err := config.Load()
	if err != nil {
		log.Printf("config load error (using defaults): %v", err)
		cfg = config.Default()
	}

	ui.Run(cfg)
}
