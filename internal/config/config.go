// Package config loads and saves application configuration from a YAML file.
// The config file is stored at $XDG_CONFIG_HOME/refconnect/config.yaml,
// falling back to ~/.config/refconnect/config.yaml.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the root application configuration struct.
type Config struct {
	Version            int              `yaml:"version"`
	Callsign           string           `yaml:"callsign"`
	CallsignSuffix     string           `yaml:"callsign_suffix"` // gateway module letter appended to callsign, e.g. "D"
	Radio              RadioConfig      `yaml:"radio"`
	Reflectors         []ReflectorEntry `yaml:"reflectors"`
	LastUsedReflector  string           `yaml:"last_used_reflector"`
	APRS               APRSConfig       `yaml:"aprs"`
	UI                 UIConfig         `yaml:"ui"`
}

// APRSConfig holds settings for DPRS (APRS over D-STAR slow data) beaconing.
// Position can come from the radio's GPS (via DPRS in the slow-data stream)
// or from a static Latitude/Longitude configured here. When both are
// available the radio GPS takes priority; when neither is set the beacon
// is skipped until a fix arrives. The beacon uses the main callsign with a
// static "-1" SSID (APRS "Primary Station").
type APRSConfig struct {
	Enabled              bool    `yaml:"enabled"`
	Symbol               string  `yaml:"symbol"`        // APRS symbol character, e.g. ">" (car), "-" (house)
	SymbolTable          string  `yaml:"symbol_table"`  // "/" (primary) or "\\" (alternate)
	Comment              string  `yaml:"comment"`       // APRS status text appended to position reports
	BeaconIntervalMinutes int    `yaml:"beacon_interval_minutes"`
	SendOnConnect        bool    `yaml:"send_on_connect"`
	Latitude             float64 `yaml:"latitude"`      // static fallback latitude (decimal degrees, + = N)
	Longitude            float64 `yaml:"longitude"`     // static fallback longitude (decimal degrees, + = E)
}

// RadioConfig holds serial port settings for the connected radio.
type RadioConfig struct {
	Port     string `yaml:"port"`
	Protocol string `yaml:"protocol"` // "DV-GW" (ICOM DV Gateway) or "MMDVM" (Kenwood MMDVM)
}

// ReflectorEntry is a saved reflector connection profile.
type ReflectorEntry struct {
	Name     string `yaml:"name"`
	Host     string `yaml:"host"`
	Port     uint16 `yaml:"port"`
	Module   string `yaml:"module"`   // single letter "A"–"Z"
	Protocol string `yaml:"protocol"` // "DExtra", "DPlus", "XLX"
}

// UIConfig holds window and display preferences.
type UIConfig struct {
	Theme        string  `yaml:"theme"`         // "dark", "light", "system"
	LogMaxLines  int     `yaml:"log_max_lines"`
	WindowWidth  float32 `yaml:"window_width"`
	WindowHeight float32 `yaml:"window_height"`
}

// LogDir returns the directory where timestamped log files are stored.
// It is a "Logs" subdirectory beside the config file.
func LogDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Logs"), nil
}

// Dir returns the platform config directory for refconnect.
func Dir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "refconnect"), nil
}

// Load reads the config file from the standard location.
// If the file does not exist, Default() is returned with no error.
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.yaml")

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		c := Default()
		return c, nil
	}
	if err != nil {
		return nil, err
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config to the standard location, creating directories as needed.
func Save(c *Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o644)
}
