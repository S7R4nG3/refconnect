package aprs

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cache is a thread-safe store for the last known GPS position, updated
// by the slow-data decoder and read by the beacon scheduler.
type Cache struct {
	mu      sync.RWMutex
	pos     Position
	seen    bool
	updated time.Time
}

// Set records a new position fix.
func (c *Cache) Set(p Position) {
	c.mu.Lock()
	c.pos = p
	c.seen = true
	c.updated = time.Now()
	c.mu.Unlock()
}

// Get returns the last position and the time it was updated. ok is false
// until a fix has been received at least once.
func (c *Cache) Get() (pos Position, updated time.Time, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pos, c.updated, c.seen
}

// ParsePosition tries to extract a GPS position from a DPRS payload or a
// raw NMEA sentence. It accepts:
//
//   - NMEA $GPRMC / $GPGGA sentences
//   - APRS TNC2 uncompressed position ("!DDMM.MMN/DDDMM.MMW>")
//
// Returns ok=false if no usable position is found.
func ParsePosition(s string) (Position, bool) {
	s = strings.TrimSpace(s)
	// NMEA sentences. Match on the RMC/GGA suffix so any talker ID works
	// ($GP, $GN, $GL, $GA, …), not just GPS.
	if len(s) >= 6 && s[0] == '$' {
		switch s[3:6] {
		case "RMC":
			return parseRMC(s)
		case "GGA":
			return parseGGA(s)
		}
	}

	// TNC2 APRS ("SOURCE>DEST,PATH:INFO") — the position is in the information
	// field, which starts after the first ':'. Parse from there so a '/' or '='
	// inside the callsign path or a "/A=" altitude extension can't be mistaken
	// for the position indicator.
	info := s
	if _, after, found := strings.Cut(s, ":"); found {
		info = after
	}
	if pos, ok := parseAPRSInfo(info); ok {
		return pos, true
	}

	// Fallback: scan for a data-type indicator anywhere, for payloads that are
	// already just the information field or use an unusual framing.
	for _, ind := range []byte{'!', '=', '/', '@'} {
		if i := strings.IndexByte(s, ind); i >= 0 {
			if pos, ok := parseAPRSInfo(s[i:]); ok {
				return pos, true
			}
		}
	}
	return Position{}, false
}

// parseAPRSInfo parses an APRS information field that begins with a data-type
// indicator, returning the uncompressed lat/lon. The indicator determines
// whether a 7-character timestamp precedes the position:
//
//	!LAT/LON…   position, no timestamp
//	=LAT/LON…   position, no timestamp, with messaging
//	/TTTTTTTz LAT/LON…   position WITH 7-char timestamp
//	@TTTTTTTz LAT/LON…   position WITH 7-char timestamp, with messaging
//
// The IC-705 emits the '!' form; the TH-D75 emits the timestamped '/' form
// (e.g. "/131335z3513.92N/08051.27W[…"), which is why skipping the timestamp
// is required for the Kenwood to decode.
func parseAPRSInfo(info string) (Position, bool) {
	if info == "" {
		return Position{}, false
	}
	switch info[0] {
	case '!', '=':
		return parseTNC2Position(info[1:])
	case '/', '@':
		if len(info) < 8 {
			return Position{}, false
		}
		return parseTNC2Position(info[8:]) // skip indicator + 7-char timestamp
	}
	return Position{}, false
}

// parseRMC extracts the position from an NMEA $GPRMC sentence.
// Format: $GPRMC,HHMMSS,A,DDMM.MMMM,N,DDDMM.MMMM,W,...
func parseRMC(s string) (Position, bool) {
	// Strip checksum if present.
	if star := strings.IndexByte(s, '*'); star >= 0 {
		s = s[:star]
	}
	fields := strings.Split(s, ",")
	if len(fields) < 7 {
		return Position{}, false
	}
	if fields[2] != "A" { // status: A=valid, V=void
		return Position{}, false
	}
	lat, ok := parseNMEACoord(fields[3], fields[4])
	if !ok {
		return Position{}, false
	}
	lon, ok := parseNMEACoord(fields[5], fields[6])
	if !ok {
		return Position{}, false
	}
	return Position{Lat: lat, Lon: lon}, true
}

// parseGGA extracts the position from an NMEA $GPGGA sentence.
// Format: $GPGGA,HHMMSS,DDMM.MMMM,N,DDDMM.MMMM,W,Q,...
func parseGGA(s string) (Position, bool) {
	if star := strings.IndexByte(s, '*'); star >= 0 {
		s = s[:star]
	}
	fields := strings.Split(s, ",")
	if len(fields) < 7 {
		return Position{}, false
	}
	if fields[6] == "0" { // fix quality: 0=invalid
		return Position{}, false
	}
	lat, ok := parseNMEACoord(fields[2], fields[3])
	if !ok {
		return Position{}, false
	}
	lon, ok := parseNMEACoord(fields[4], fields[5])
	if !ok {
		return Position{}, false
	}
	return Position{Lat: lat, Lon: lon}, true
}

// parseNMEACoord converts a DDMM.MMMM (lat) or DDDMM.MMMM (lon) string +
// hemisphere letter into signed decimal degrees.
func parseNMEACoord(raw, hemi string) (float64, bool) {
	if raw == "" || hemi == "" {
		return 0, false
	}
	dot := strings.IndexByte(raw, '.')
	if dot < 2 {
		return 0, false
	}
	degEnd := dot - 2 // the last 2 digits before the dot are whole minutes
	if degEnd < 0 {
		return 0, false
	}
	degStr := raw[:degEnd]
	minStr := raw[degEnd:]
	deg, err := strconv.ParseFloat(degStr, 64)
	if err != nil {
		return 0, false
	}
	min, err := strconv.ParseFloat(minStr, 64)
	if err != nil {
		return 0, false
	}
	val := deg + min/60.0
	switch hemi {
	case "S", "W":
		val = -val
	}
	return val, true
}

// parseTNC2Position parses the APRS uncompressed position that follows a
// data-type indicator, e.g. "3340.00N/08425.00W>RefConnect".
func parseTNC2Position(s string) (Position, bool) {
	if len(s) < 19 {
		return Position{}, false
	}
	latRaw := s[0:7] // "DDMM.mm"
	latHemi := string(s[7])
	// s[8] = symbol table
	lonRaw := s[9:17] // "DDDMM.mm"
	lonHemi := string(s[17])
	lat, ok := parseNMEACoord(latRaw, latHemi)
	if !ok {
		return Position{}, false
	}
	lon, ok := parseNMEACoord(lonRaw, lonHemi)
	if !ok {
		return Position{}, false
	}
	return Position{Lat: lat, Lon: lon}, true
}
