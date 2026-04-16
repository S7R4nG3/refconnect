package config

// Default returns a Config populated with sensible out-of-the-box defaults.
func Default() *Config {
	return &Config{
		Version:        1,
		Callsign:       "N0CALL",
		CallsignSuffix: "G",
		Radio: RadioConfig{
			Port:     "/dev/ttyUSB0",
			Protocol: "DV-GW",
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
		APRS: APRSConfig{
			Enabled:               false,
			Symbol:                ">",
			SymbolTable:           "/",
			Comment:               "RefConnect D-STAR",
			BeaconIntervalMinutes: 30,
			SendOnConnect:         true,
		},
		UI: UIConfig{
			Theme:        "system",
			LogMaxLines:  500,
			WindowWidth:  960,
			WindowHeight: 720,
		},
	}
}
