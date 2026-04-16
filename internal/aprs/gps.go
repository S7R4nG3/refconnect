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
	switch {
	case strings.HasPrefix(s, "$GPRMC"):
		return parseRMC(s)
	case strings.HasPrefix(s, "$GPGGA"):
		return parseGGA(s)
	}
	// Look for TNC2 APRS position anywhere in the payload. The data-type
	// indicators that carry a bare lat/lon are '!', '=', '/', '@'.
	for _, ind := range []byte{'!', '=', '/', '@'} {
		if i := strings.IndexByte(s, ind); i >= 0 {
			if pos, ok := parseTNC2Position(s[i+1:]); ok {
				return pos, true
			}
		}
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
