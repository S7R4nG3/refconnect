package dplus

import (
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// fakeAuthServer accepts one login per connection, records it, and replies
// with one batch holding the given records.
func fakeAuthServer(t testing.TB, records [][26]byte, got chan<- []byte) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				req := make([]byte, 56)
				if _, err := io.ReadFull(c, req); err != nil {
					return
				}
				if got != nil {
					got <- req
				}
				n := 8 + 26*len(records)
				body := make([]byte, n)
				body[0], body[1], body[2] = byte(n), 0xC0|byte(n>>8), 0x01
				for i, r := range records {
					copy(body[8+26*i:], r[:])
				}
				c.Write(body) //nolint:errcheck
			}(c)
		}
	}()
	return ln.Addr().String()
}

func record(addr, name string, active bool) [26]byte {
	var r [26]byte
	copy(r[0:16], addr+"                ")
	copy(r[16:24], name+"        ")
	if active {
		r[25] = 0x80
	}
	return r
}

func TestMain(m *testing.M) {
	// Never reach the real auth server from tests.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	authAddr = ln.Addr().String()
	os.Exit(m.Run())
}

func TestBaseCallsign(t *testing.T) {
	for in, want := range map[string]string{
		"KR4GCQ D": "KR4GCQ",
		"KR4GCQ  ": "KR4GCQ",
		"W1AW":     "W1AW",
		"KR4GCQAB": "KR4GCQAB",
	} {
		if got := baseCallsign(in); got != want {
			t.Errorf("baseCallsign(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	got := make(chan []byte, 1)
	addr := fakeAuthServer(t, [][26]byte{
		record("1.2.3.4", "REF001", true),
		record("5.6.7.8", "REF002", false),
		record("9.9.9.9", "XRF003", true),
	}, got)
	saved := authAddr
	authAddr = addr
	defer func() { authAddr = saved }()

	refs, err := authenticate("KR4GCQ")
	if err != nil {
		t.Fatal(err)
	}
	if refs != 1 {
		t.Errorf("refs = %d, want 1", refs)
	}
	req := <-got
	want := []byte{0x38, 0xC0, 0x01, 0x00}
	want = append(want, "KR4GCQ  DV019999        W7IB2       DHS0257         "...)
	if string(req) != string(want) {
		t.Errorf("request =\n% X\nwant\n% X", req, want)
	}
}

func TestEnsureAuthenticatedCaches(t *testing.T) {
	got := make(chan []byte, 4)
	saved := authAddr
	authAddr = fakeAuthServer(t, nil, got)
	authCall, authAt = "", time.Time{}
	defer func() { authAddr = saved; authCall, authAt = "", time.Time{} }()

	ensureAuthenticated("KR4GCQ D")
	ensureAuthenticated("KR4GCQ D")
	if len(got) != 1 {
		t.Fatalf("auth requests = %d, want 1 (second should be cached)", len(got))
	}
}
