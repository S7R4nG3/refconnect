package config

// Default returns a Config populated with sensible out-of-the-box defaults.
func Default() *Config {
	return &Config{
		Version:        1,
		Callsign:       "N0CALL",
		CallsignSuffix: " ",
		Radio: RadioConfig{
			Port:      "/dev/ttyUSB0",
			BaudRate:  38400,
			DataBits:  8,
			StopBits:  1,
			Parity:    "N",
			PTTViaRTS: false,
		},
		Reflectors: []ReflectorEntry{
			{
				Name:     "REF001 C",
				Host:     "ref001.dstargateway.org",
				Port:     20001,
				Module:   "C",
				Protocol: "DPlus",
			},
		},
		UI: UIConfig{
			Theme:        "system",
			LogMaxLines:  500,
			WindowWidth:  960,
			WindowHeight: 720,
		},
	}
}
