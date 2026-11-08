package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestFrom(t *testing.T) {
	cases := []struct {
		name, remote, xff, want string
	}{
		{"no header uses peer", "172.18.0.5:4444", "", "172.18.0.5"},
		{"single hop", "172.18.0.5:4444", "203.0.113.7", "203.0.113.7"},
		{"leftmost of many", "172.18.0.5:4444", "203.0.113.7, 104.16.0.1", "203.0.113.7"},
		{"spaces trimmed", "172.18.0.5:4444", "  203.0.113.7  ", "203.0.113.7"},
		{"ipv6", "172.18.0.5:4444", "2001:db8::1", "2001:db8::1"},
		{"ipv6 with port", "172.18.0.5:4444", "[2001:db8::1]:5555", "2001:db8::1"},
		{"ipv4 with port", "172.18.0.5:4444", "203.0.113.7:5555", "203.0.113.7"},
		{"garbage falls back", "172.18.0.5:4444", "not-an-ip", "172.18.0.5"},
		{"empty first falls back", "172.18.0.5:4444", ", 203.0.113.7", "172.18.0.5"},
		{"peer without port", "172.18.0.5", "", "172.18.0.5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.remote
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := From(r); got != c.want {
				t.Fatalf("From = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFromJoinsRepeatedHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:1"
	r.Header.Add("X-Forwarded-For", "203.0.113.7")
	r.Header.Add("X-Forwarded-For", "198.51.100.1")
	if got := From(r); got != "203.0.113.7" {
		t.Fatalf("From = %q", got)
	}
}
