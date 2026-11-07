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

// With two case variants of one key the form would show one and Traefik read
// the other, so the form is not offered until the YAML is fixed.
func TestReadFormRefusesDuplicateKeys(t *testing.T) {
	if _, _, err := ReadForm([]byte("log: {level: DEBUG}\nLog: {level: ERROR}\n")); err == nil {
		t.Fatal("ReadForm accepted duplicate case-variant keys")
	}
	if _, _, err := WriteForm([]byte("log: {level: DEBUG}\nLog: {level: ERROR}\n"), Form{LogLevel: "WARN"}); err == nil {
		t.Fatal("WriteForm accepted duplicate case-variant keys")
	}
}

// A comment-only override whose last line has no newline (the editor allows
// it) must not swallow the new keys into that comment.
func TestWriteFormCommentOnlyWithoutTrailingNewline(t *testing.T) {
	out := writeForm(t, "# mine", Form{LogLevel: "DEBUG"})
	f, problems := readForm(t, out)
	if len(problems) != 0 || f.LogLevel != "DEBUG" {
		t.Fatalf("read back %q: form %+v, problems %+v", out, f, problems)
	}
	if !strings.HasPrefix(out, "# mine\n") {
		t.Fatalf("comment not kept on its own line: %q", out)
	}
}

var readTimeoutPath = []string{"entryPoints", "websecure", "transport", "respondingTimeouts", "readTimeout"}

func readTimeoutAt(t *testing.T, out, ep string) (string, bool) {
	t.Helper()
	m, err := parseMap([]byte(out))
	if err != nil {
		t.Fatalf("parse %q: %v", out, err)
	}
	_, v := lookup(m, "entryPoints", ep, "transport", "respondingTimeouts", "readTimeout")
	if v == nil {
		return "", false
	}
	return v.Value, true
}

func TestReadFormReadTimeoutDefaultsToBase(t *testing.T) {
	f, _ := readForm(t, "")
	if f.ReadTimeout != DefaultReadTimeout || DefaultReadTimeout != "3600s" {
		t.Fatalf("ReadTimeout = %q, DefaultReadTimeout = %q", f.ReadTimeout, DefaultReadTimeout)
	}
}

func TestReadFormReadTimeoutFromWebsecure(t *testing.T) {
	f, _ := readForm(t, "entryPoints:\n  websecure:\n    transport:\n      respondingTimeouts:\n        readTimeout: 5m\n")
	if f.ReadTimeout != "5m" {
		t.Fatalf("ReadTimeout = %q, want 5m", f.ReadTimeout)
	}
}

func TestReadFormWarnsWhenReadTimeoutsDiffer(t *testing.T) {
	in := "entryPoints:\n  web:\n    transport:\n      respondingTimeouts:\n        readTimeout: 1h\n" +
		"  websecure:\n    transport:\n      respondingTimeouts:\n        readTimeout: 5m\n"
	f, warnings := readForm(t, in)
	if f.ReadTimeout != "5m" {
		t.Fatalf("ReadTimeout = %q, want websecure's 5m", f.ReadTimeout)
	}
	if !slices.ContainsFunc(warnings, func(p Problem) bool {
		return p.Key == "entryPoints.web.transport.respondingTimeouts.readTimeout"
	}) {
		t.Fatalf("warnings = %+v", warnings)
	}
}

func hasTimeoutWarning(warnings []Problem) bool {
	return slices.ContainsFunc(warnings, func(p Problem) bool {
		return p.Key == "entryPoints.web.transport.respondingTimeouts.readTimeout"
	})
}

// A key on one entrypoint differs from base's 3600s on the other: saving the
// form would copy it across, so the form says so.
func TestReadFormWarnsWhenOnlyOneEntryPointHasReadTimeout(t *testing.T) {
	for _, ep := range []string{"web", "websecure"} {
		f, warnings := readForm(t, "entryPoints:\n  "+ep+":\n    transport:\n      respondingTimeouts:\n        readTimeout: 5m\n")
		if f.ReadTimeout != "5m" || !hasTimeoutWarning(warnings) {
			t.Errorf("only %s: ReadTimeout = %q, warnings = %+v", ep, f.ReadTimeout, warnings)
		}
	}
}

func TestReadFormNoWarningWhenReadTimeoutsMeanTheSame(t *testing.T) {
	for _, in := range []string{
		"entryPoints:\n  web:\n    transport:\n      respondingTimeouts:\n        readTimeout: 1h\n" +
			"  websecure:\n    transport:\n      respondingTimeouts:\n        readTimeout: 3600s\n",
		"entryPoints:\n  websecure:\n    transport:\n      respondingTimeouts:\n        readTimeout: 60m\n",
	} {
		if _, warnings := readForm(t, in); hasTimeoutWarning(warnings) {
			t.Errorf("warnings = %+v for\n%s", warnings, in)
		}
	}
}

func TestWriteFormReadTimeoutBothEntryPoints(t *testing.T) {
	out := writeForm(t, "", Form{LogLevel: "INFO", ReadTimeout: "10m"})
	for _, ep := range []string{"web", "websecure"} {
		if v, ok := readTimeoutAt(t, out, ep); !ok || v != "10m" {
			t.Errorf("%s readTimeout = %q, %v in\n%s", ep, v, ok, out)
		}
	}
}

func TestWriteFormReadTimeoutDefaultRemovesKey(t *testing.T) {
	set := writeForm(t, "", Form{LogLevel: "INFO", ReadTimeout: "10m"})
	for _, def := range []string{"3600s", "1h", "3600"} {
		out := writeForm(t, set, Form{LogLevel: "INFO", ReadTimeout: def})
		if strings.Contains(out, "readTimeout") || strings.Contains(out, "respondingTimeouts") || strings.Contains(out, "transport") {
			t.Errorf("%s left the key or its empty parents:\n%s", def, out)
		}
	}
}

func TestWriteFormEmptyReadTimeoutKeepsOverride(t *testing.T) {
	set := writeForm(t, "", Form{LogLevel: "INFO", ReadTimeout: "10m"})
	out := writeForm(t, set, Form{LogLevel: "INFO"})
	if v, ok := readTimeoutAt(t, out, "websecure"); !ok || v != "10m" {
		t.Fatalf("readTimeout = %q, %v in\n%s", v, ok, out)
	}
}

func TestWriteFormRejectsShortReadTimeout(t *testing.T) {
	for _, bad := range []string{"29s", "0", "0s", "abc", "-5m"} {
		_, problems, err := WriteForm(nil, Form{LogLevel: "INFO", ReadTimeout: bad})
		if err != nil {
			t.Fatalf("%s: err = %v", bad, err)
		}
		want := Problem{Key: strings.Join(readTimeoutPath, "."), Message: "read timeout must be a duration of at least 30s, like 90s, 5m or 1h"}
		if len(problems) != 1 || problems[0] != want {
			t.Errorf("%s: problems = %+v", bad, problems)
		}
	}
}

func TestWriteFormAcceptsBareSeconds(t *testing.T) {
	out := writeForm(t, "", Form{LogLevel: "INFO", ReadTimeout: " 120 "})
	if v, ok := readTimeoutAt(t, out, "websecure"); !ok || v != "120s" {
		t.Fatalf("readTimeout = %q, %v in\n%s", v, ok, out)
	}
}

func TestWriteFormReadTimeoutReachesGenerated(t *testing.T) {
	out := writeForm(t, "", Form{LogLevel: "INFO", ReadTimeout: "5m"})
	eff, problems, err := Generate([]byte(out))
	if err != nil || len(problems) != 0 {
		t.Fatalf("Generate: %v %+v", err, problems)
	}
	if v, ok := readTimeoutAt(t, string(eff), "web"); !ok || v != "5m" {
		t.Fatalf("generated web readTimeout = %q, %v", v, ok)
	}
}

// An edit that leaves the read timeout as shown does not touch it: a
// hand-written short value, its comment and its spelling survive, and it does
// not block the save.
func TestWriteFormLeavesUnchangedReadTimeoutAlone(t *testing.T) {
	override := `entryPoints:
  web:
    transport:
      respondingTimeouts:
        readTimeout: 10s # short on purpose
  websecure:
    transport:
      respondingTimeouts:
        readTimeout: 10s # short on purpose
`
	f, _, err := ReadForm([]byte(override))
	if err != nil {
		t.Fatal(err)
	}
	f.LogLevel = "DEBUG"
	out, problems, err := WriteForm([]byte(override), f)
	if err != nil || len(problems) != 0 {
		t.Fatalf("problems = %+v, err = %v", problems, err)
	}
	if strings.Count(string(out), "readTimeout: 10s # short on purpose") != 2 {
		t.Fatalf("read timeout rewritten:\n%s", out)
	}
	// Changed, it is checked and written.
	f.ReadTimeout = "15s"
	if _, problems, _ := WriteForm([]byte(override), f); len(problems) != 1 {
		t.Fatalf("a new short value passed: %+v", problems)
	}
}

func TestReadTimeoutThatIsNotADurationIsKept(t *testing.T) {
	override := "entryPoints:\n  websecure:\n    transport:\n      respondingTimeouts:\n        readTimeout: [1h]\n"
	f, warnings, err := ReadForm([]byte(override))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(warnings, func(p Problem) bool { return strings.Contains(p.Message, "not a duration") }) {
		t.Fatalf("warnings = %+v", warnings)
	}
	f.LogLevel = "DEBUG"
	out, problems, err := WriteForm([]byte(override), f)
	if err != nil || len(problems) != 0 {
		t.Fatalf("problems = %+v, err = %v", problems, err)
	}
	if !strings.Contains(string(out), "readTimeout: [1h]") {
		t.Fatalf("a list read timeout was dropped:\n%s", out)
	}
}

// A null read timeout would pass over base's 3600s; Settings says so and
// saving removes it.
func TestNullReadTimeoutWarnsAndSaveRemovesIt(t *testing.T) {
	override := "entryPoints:\n  websecure:\n    transport:\n      respondingTimeouts:\n        readTimeout: null\n"
	f, warnings, err := ReadForm([]byte(override))
	if err != nil {
		t.Fatal(err)
	}
	if f.ReadTimeout != DefaultReadTimeout || !slices.ContainsFunc(warnings, func(p Problem) bool { return strings.Contains(p.Message, "null") }) {
		t.Fatalf("form = %q, warnings = %+v", f.ReadTimeout, warnings)
	}
	out := writeForm(t, override, f)
	if strings.Contains(out, "readTimeout") {
		t.Fatalf("null read timeout kept:\n%s", out)
	}
}

func TestRemovingReadTimeoutKeepsSiblings(t *testing.T) {
	override := "entryPoints:\n  web:\n    transport:\n      respondingTimeouts:\n        readTimeout: 10m\n        writeTimeout: 5m\n  websecure:\n    transport:\n      respondingTimeouts:\n        readTimeout: 10m\n        writeTimeout: 5m\n"
	out := writeForm(t, override, Form{LogLevel: "INFO", ReadTimeout: "1h"})
	if strings.Contains(out, "readTimeout") || strings.Count(out, "writeTimeout: 5m") != 2 {
		t.Fatalf("out:\n%s", out)
	}
}
