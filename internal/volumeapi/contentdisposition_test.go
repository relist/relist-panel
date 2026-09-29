package volumeapi

import (
	"mime"
	"testing"
)

// contentDisposition is the one RFC 6266 shape both download routes use:
// control characters stripped, quotes escaped, path separators — "/" and the
// Windows "\" — neutralized, and filename* percent-encoded for anything
// non-ASCII. Every header parses back to the sanitized name.
func TestContentDispositionFollowsRFC6266(t *testing.T) {
	tests := []struct {
		name     string
		download string
		want     string
		decoded  string
	}{
		{
			name:     "plain ascii",
			download: "data.tar.gz",
			want:     `attachment; filename="data.tar.gz"`,
			decoded:  "data.tar.gz",
		},
		{
			name:     "spaces",
			download: "an example.html",
			want:     `attachment; filename="an example.html"`,
			decoded:  "an example.html",
		},
		{
			name:     "quotes are escaped and backslashes are separators too",
			download: `we"ird\name.tar.gz`,
			want:     `attachment; filename="we\"ird_name.tar.gz"`,
			decoded:  `we"ird_name.tar.gz`,
		},
		{
			name:     "control characters are stripped",
			download: "tab\tnew\nline\rname.tar.gz",
			want:     `attachment; filename="tabnewlinename.tar.gz"`,
			decoded:  "tabnewlinename.tar.gz",
		},
		{
			name:     "non-ascii gets a percent-encoded filename*",
			download: "café.tar.gz",
			want:     `attachment; filename="caf_.tar.gz"; filename*=utf-8''caf%c3%a9.tar.gz`,
			decoded:  "café.tar.gz",
		},
		{
			name:     "quotes and spaces are encoded inside filename*",
			download: "it's café.tar.gz",
			want:     `attachment; filename="it's caf_.tar.gz"; filename*=utf-8''it%27s%20caf%c3%a9.tar.gz`,
			decoded:  "it's café.tar.gz",
		},
		{
			name:     "path separators can never survive",
			download: `/etc/pa\sswd`,
			want:     `attachment; filename="_etc_pa_sswd"`,
			decoded:  "_etc_pa_sswd",
		},
		{
			name:     "a name that sanitizes to nothing usable falls back",
			download: "..",
			want:     `attachment; filename="download"`,
			decoded:  "download",
		},
		{
			name:     "an empty name falls back too",
			download: "",
			want:     `attachment; filename="download"`,
			decoded:  "download",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := contentDisposition(tt.download)
			if got != tt.want {
				t.Errorf("contentDisposition(%q) = %q, want %q", tt.download, got, tt.want)
			}
			_, params, err := mime.ParseMediaType(got)
			if err != nil {
				t.Fatalf("mime.ParseMediaType(%q): %v", got, err)
			}
			if params["filename"] != tt.decoded {
				t.Errorf("parsed filename = %q, want %q", params["filename"], tt.decoded)
			}
		})
	}
}
