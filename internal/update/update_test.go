package update

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.13.0", "v0.12.0", true},
		{"v0.12.1", "v0.12.0", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.12.0", "v0.12.0", false},
		{"v0.11.9", "v0.12.0", false},
		{"v0.10.0", "v0.9.0", true},
		{"0.12.0", "v0.12.0", false},
		{"v0.13.0-rc1", "v0.12.0", true},
	}
	for _, c := range cases {
		if got := isNewer(c.a, c.b); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestInstallLocation(t *testing.T) {
	dir, relaunch := installLocation("/opt/refconnect/refconnect")
	if dir != "/opt/refconnect" || relaunch != "/opt/refconnect/refconnect" {
		t.Errorf("plain binary: got (%q, %q)", dir, relaunch)
	}
}
