package aprs

import (
	"math"
	"strings"
	"testing"
)

func TestFormatLatLon(t *testing.T) {
	if got, want := formatLat(33.6667), "3340.00N"; got != want {
		t.Errorf("formatLat(33.6667) = %q, want %q", got, want)
	}
	if got, want := formatLat(-33.6667), "3340.00S"; got != want {
		t.Errorf("formatLat(-33.6667) = %q, want %q", got, want)
	}
	if got, want := formatLon(-84.4167), "08425.00W"; got != want {
		t.Errorf("formatLon(-84.4167) = %q, want %q", got, want)
	}
	if got, want := formatLon(5.25), "00515.00E"; got != want {
		t.Errorf("formatLon(5.25) = %q, want %q", got, want)
	}
}

func TestBuildPositionPacket(t *testing.T) {
	got := BuildPositionPacket("KR4GCQ", Position{Lat: 33.6667, Lon: -84.4167}, '/', '>', "RefConnect")
	want := "KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect"
	if got != want {
		t.Errorf("BuildPositionPacket() = %q, want %q", got, want)
	}
}

func TestDPRSRoundTrip(t *testing.T) {
	payload := "KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect"
	wrapped := WrapDPRS(payload)
	if !strings.HasPrefix(wrapped, "$$CRC") || !strings.HasSuffix(wrapped, "\r") {
		t.Fatalf("WrapDPRS: wrong framing: %q", wrapped)
	}
	got, ok := ValidateDPRS(wrapped)
	if !ok {
		t.Fatalf("ValidateDPRS rejected valid sentence: %q", wrapped)
	}
	if got != payload {
		t.Errorf("ValidateDPRS payload = %q, want %q", got, payload)
	}
}

func TestValidateDPRSBadCRC(t *testing.T) {
	// Flip a byte in the body — CRC should no longer match.
	bad := "$$CRC0000,KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect\r"
	if _, ok := ValidateDPRS(bad); ok {
		t.Error("ValidateDPRS accepted a sentence with wrong CRC")
	}
}

func TestParseRMC(t *testing.T) {
	s := "$GPRMC,001423.000,A,3340.0000,N,08425.0000,W,000.0,000.0,010126,,A*72"
	pos, ok := ParsePosition(s)
	if !ok {
		t.Fatalf("ParsePosition rejected valid RMC: %q", s)
	}
	if math.Abs(pos.Lat-33.6667) > 0.001 {
		t.Errorf("RMC lat = %v, want ~33.6667", pos.Lat)
	}
	if math.Abs(pos.Lon-(-84.4167)) > 0.001 {
		t.Errorf("RMC lon = %v, want ~-84.4167", pos.Lon)
	}
}

func TestParseTNC2(t *testing.T) {
	s := "KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect"
	pos, ok := ParsePosition(s)
	if !ok {
		t.Fatalf("ParsePosition rejected valid TNC2: %q", s)
	}
	if math.Abs(pos.Lat-33.6667) > 0.001 {
		t.Errorf("TNC2 lat = %v, want ~33.6667", pos.Lat)
	}
	if math.Abs(pos.Lon-(-84.4167)) > 0.001 {
		t.Errorf("TNC2 lon = %v, want ~-84.4167", pos.Lon)
	}
}

func TestCache(t *testing.T) {
	var c Cache
	if _, _, ok := c.Get(); ok {
		t.Fatal("fresh cache should report ok=false")
	}
	p := Position{Lat: 10, Lon: 20}
	c.Set(p)
	got, _, ok := c.Get()
	if !ok || got != p {
		t.Errorf("after Set, Get = (%v,%v), want (%v,true)", got, ok, p)
	}
}
