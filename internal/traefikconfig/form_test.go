package traefikconfig

import (
	"net"
	"slices"
	"strings"
	"testing"
)

func writeForm(t *testing.T, override string, f Form) string {
	t.Helper()
	out, problems, err := WriteForm([]byte(override), f)
	if err != nil || len(problems) != 0 {
		t.Fatalf("WriteForm: err = %v, problems = %+v", err, problems)
	}
	return string(out)
}

func readForm(t *testing.T, override string) (Form, []Problem) {
	t.Helper()
	f, warnings, err := ReadForm([]byte(override))
	if err != nil {
		t.Fatalf("ReadForm: %v", err)
	}
	return f, warnings
}

func TestWriteFormKeepsComments(t *testing.T) {
	in := "# keep me\napi:\n  insecure: false\n"
	out := writeForm(t, in, Form{ACMEEmail: "a@b.c", LogLevel: "INFO"})
	keep, api, email := strings.Index(out, "# keep me"), strings.Index(out, "api:"), strings.Index(out, "a@b.c")
	if keep < 0 || api < 0 || email < 0 || !(keep < api && api < email) {
		t.Fatalf("out =\n%s", out)
	}
}

func TestWriteFormTrustedIPsBoth(t *testing.T) {
	out := writeForm(t, "", Form{TrustedIPs: []string{"1.2.3.4"}, LogLevel: "INFO"})
	m, _ := parseMap([]byte(out))
	for _, ep := range []string{"web", "websecure"} {
		_, v := lookup(m, "entryPoints", ep, "forwardedHeaders", "trustedIPs")
		if v == nil || len(v.Content) != 1 || v.Content[0].Value != "1.2.3.4" {
			t.Errorf("%s = %v in\n%s", ep, v, out)
		}
	}
}

func TestCloudflareOnOffDetect(t *testing.T) {
	on := writeForm(t, "", Form{TrustedIPs: []string{"1.2.3.4"}, TrustCloudflare: true, LogLevel: "INFO"})
	eff, _, _ := Generate([]byte(on))
	got := TrustedIPs(eff)
	if want := append([]string{"1.2.3.4"}, CloudflareRanges...); !slices.Equal(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
	f, _ := readForm(t, on)
	if !f.TrustCloudflare || !slices.Equal(f.TrustedIPs, []string{"1.2.3.4"}) {
		t.Fatalf("read back = %+v", f)
	}
	off := writeForm(t, on, Form{TrustedIPs: f.TrustedIPs, TrustCloudflare: false, LogLevel: "INFO"})
	eff, _, _ = Generate([]byte(off))
	if got := TrustedIPs(eff); !slices.Equal(got, []string{"1.2.3.4"}) {
		t.Fatalf("after off = %v", got)
	}
	partial := writeForm(t, "", Form{TrustedIPs: CloudflareRanges[1:], LogLevel: "INFO"})
	f, _ = readForm(t, partial)
	if f.TrustCloudflare || !slices.Equal(f.TrustedIPs, CloudflareRanges[1:]) {
		t.Fatalf("partial = %+v", f)
	}
}

func TestFormAccessLog(t *testing.T) {
	on := writeForm(t, "", Form{AccessLog: true, LogLevel: "INFO"})
	if f, _ := readForm(t, on); !f.AccessLog {
		t.Fatalf("on = %s", on)
	}
	off := writeForm(t, "accessLog:\n  fields:\n    names:\n      ClientUsername: drop\n", Form{AccessLog: false, LogLevel: "INFO"})
	if strings.Contains(strings.ToLower(off), "accesslog") {
		t.Fatalf("off = %s", off)
	}
}

func TestFormLogLevel(t *testing.T) {
	for _, lvl := range []string{"ERROR", "WARN", "INFO", "DEBUG"} {
		out := writeForm(t, "log: {level: INFO}\n", Form{LogLevel: lvl})
		if f, _ := readForm(t, out); f.LogLevel != lvl {
			t.Errorf("%s read back as %s", lvl, f.LogLevel)
		}
	}
	_, problems, err := WriteForm(nil, Form{LogLevel: "TRACE"})
	if err != nil || len(problems) != 1 || problems[0].Key != "log.level" {
		t.Fatalf("TRACE: err = %v, problems = %+v", err, problems)
	}
	if f, _ := readForm(t, ""); f.LogLevel != "INFO" {
		t.Errorf("empty level = %q", f.LogLevel)
	}
}

func TestFormBadIP(t *testing.T) {
	_, problems, err := WriteForm(nil, Form{TrustedIPs: []string{"x"}, LogLevel: "INFO"})
	if err != nil || len(problems) != 1 {
		t.Fatalf("err = %v, problems = %+v", err, problems)
	}
}

func TestReadFormCaseVariants(t *testing.T) {
	in := "entrypoints:\n  websecure:\n    forwardedheaders:\n      trustedips: [1.2.3.4]\n"
	f, _ := readForm(t, in)
	if !slices.Equal(f.TrustedIPs, []string{"1.2.3.4"}) {
		t.Fatalf("TrustedIPs = %v", f.TrustedIPs)
	}
	out := writeForm(t, in, Form{TrustedIPs: []string{"5.6.7.8"}, LogLevel: "INFO"})
	if strings.Count(strings.ToLower(out), "entrypoints:") != 1 || strings.Count(strings.ToLower(out), "forwardedheaders:") != 2 {
		t.Fatalf("out =\n%s", out)
	}
	if f, _ := readForm(t, out); !slices.Equal(f.TrustedIPs, []string{"5.6.7.8"}) {
		t.Fatalf("read back = %v", f.TrustedIPs)
	}
}

func TestReadFormListsDiffer(t *testing.T) {
	in := "entryPoints:\n  web: {forwardedHeaders: {trustedIPs: [1.1.1.1]}}\n  websecure: {forwardedHeaders: {trustedIPs: [2.2.2.2]}}\n"
	f, warnings := readForm(t, in)
	if !slices.Equal(f.TrustedIPs, []string{"2.2.2.2"}) {
		t.Errorf("TrustedIPs = %v, want websecure's", f.TrustedIPs)
	}
	if len(warnings) != 1 || warnings[0].Key != "entryPoints.web.forwardedHeaders.trustedIPs" ||
		warnings[0].Message != "web and websecure differ; saving here makes them the same" {
		t.Fatalf("warnings = %+v", warnings)
	}
}

func TestCloudflareRangesValid(t *testing.T) {
	var v4, v6 bool
	for _, r := range CloudflareRanges {
		ip, _, err := net.ParseCIDR(r)
		if err != nil {
			t.Errorf("%s: %v", r, err)
			continue
		}
		if ip.To4() != nil {
			v4 = true
		} else {
			v6 = true
		}
	}
	if !v4 || !v6 {
		t.Errorf("v4 = %v, v6 = %v", v4, v6)
	}
}
