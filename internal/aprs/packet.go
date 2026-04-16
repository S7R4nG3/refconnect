// Package aprs builds APRS packets and wraps them for transport over
// D-STAR slow-data (DPRS). It also parses inbound DPRS / NMEA position
// reports so the application can cache the radio's current GPS fix.
package aprs

import (
	"fmt"
	"math"
	"strings"
)

// Position is a decoded GPS fix used for APRS position reports.
type Position struct {
	Lat float64 // decimal degrees, positive north
	Lon float64 // decimal degrees, positive east
}

// BuildPositionPacket returns a TNC2-format APRS position report, e.g.
//
//	KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect D-STAR
//
// src is the base callsign (without SSID); a static "-1" SSID is appended
// per the recommended "Primary Station (generic)" APRS convention.
// symTable is usually "/" (primary) or "\\" (alternate); symChar is the
// single-byte APRS symbol (e.g. ">" for car).
func BuildPositionPacket(src string, pos Position, symTable, symChar byte, comment string) string {
	call := strings.ToUpper(strings.TrimSpace(src))
	latStr := formatLat(pos.Lat)
	lonStr := formatLon(pos.Lon)
	// Uncompressed position format: !lat<sym_table>lon<sym_char>comment
	return fmt.Sprintf("%s-1>APDPRS,DSTAR*:!%s%c%s%c%s",
		call, latStr, symTable, lonStr, symChar, comment)
}

// formatLat encodes a decimal latitude as DDMM.mmH (H = "N" or "S"), 8 chars.
func formatLat(lat float64) string {
	hemi := byte('N')
	if lat < 0 {
		hemi = 'S'
		lat = -lat
	}
	deg := int(math.Floor(lat))
	min := (lat - float64(deg)) * 60
	return fmt.Sprintf("%02d%05.2f%c", deg, min, hemi)
}

// formatLon encodes a decimal longitude as DDDMM.mmH (H = "E" or "W"), 9 chars.
func formatLon(lon float64) string {
	hemi := byte('E')
	if lon < 0 {
		hemi = 'W'
		lon = -lon
	}
	deg := int(math.Floor(lon))
	min := (lon - float64(deg)) * 60
	return fmt.Sprintf("%03d%05.2f%c", deg, min, hemi)
}
