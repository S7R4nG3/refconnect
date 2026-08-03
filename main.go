package main

import (
	"log"
	"os"
	"strconv"
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
			// Prune leftover empty log files before creating a new one.
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

	// Apply the configured UI scale via FYNE_SCALE before the Fyne app is
	// created (Fyne reads the env var only at startup). A value already set
	// in the environment takes precedence so an explicit
	// `FYNE_SCALE=... ./refconnect` still wins. A config scale of 0 means
	// "auto" — leave detection to Fyne.
	if cfg.UI.Scale > 0 {
		if _, ok := os.LookupEnv("FYNE_SCALE"); !ok {
			os.Setenv("FYNE_SCALE", strconv.FormatFloat(float64(cfg.UI.Scale), 'f', -1, 32))
		}
	}

	ui.Run(cfg)
}
