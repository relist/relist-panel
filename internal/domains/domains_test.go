package domains_test

import (
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/domains"
)

func validInput() domains.Input {
	return domains.Input{Host: "api.example.com", Path: "/", InternalPath: "/", Service: "web", Port: 3000}
}

func TestValidateNormalisesAndDefaults(t *testing.T) {
	in := domains.Input{Host: " API.Example.com ", Service: "web", Port: 80, HTTPS: false, CertProvider: "custom", CertResolver: "x"}
	got, fe := domains.Validate(in, []string{"web"})
	if fe != nil {
		t.Fatalf("Validate error = %+v", fe)
	}
	if got.Host != "api.example.com" || got.Path != "/" || got.InternalPath != "/" {
		t.Errorf("host/paths = %q %q %q", got.Host, got.Path, got.InternalPath)
	}
	if got.CertProvider != "none" || got.CertResolver != "" {
		t.Errorf("cert = %q %q, want none and empty", got.CertProvider, got.CertResolver)
	}
	if got.Middlewares == nil || len(got.Middlewares) != 0 {
		t.Errorf("middlewares = %#v, want empty non-nil", got.Middlewares)
	}
}

func TestValidateKeepsCustomResolverOnlyWhenHTTPS(t *testing.T) {
	in := validInput()
	in.HTTPS, in.CertProvider, in.CertResolver = true, "letsencrypt", "ignored"
	got, fe := domains.Validate(in, []string{"web"})
	if fe != nil {
		t.Fatal(fe)
	}
	if got.CertProvider != "letsencrypt" || got.CertResolver != "" {
		t.Errorf("cert = %q %q", got.CertProvider, got.CertResolver)
	}
	in.CertProvider, in.CertResolver = "custom", "myresolver"
	got, fe = domains.Validate(in, []string{"web"})
	if fe != nil || got.CertResolver != "myresolver" {
		t.Errorf("custom = %+v %+v", got, fe)
	}
}

func TestValidateRejects(t *testing.T) {
	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = "m"
	}
	cases := []struct {
		name   string
		mutate func(*domains.Input)
		field  string
	}{
		{"trailing dot host", func(i *domains.Input) { i.Host = "api.example.com." }, "host"},
		{"wildcard host", func(i *domains.Input) { i.Host = "*.example.com" }, "host"},
		{"empty host", func(i *domains.Input) { i.Host = " " }, "host"},
		{"host with port", func(i *domains.Input) { i.Host = "a.com:80" }, "host"},
		{"path with space", func(i *domains.Input) { i.Path = "/a b" }, "path"},
		{"path with backtick", func(i *domains.Input) { i.Path = "/a`b" }, "path"},
		{"path with dollar", func(i *domains.Input) { i.Path = "/a$b" }, "path"},
		{"path without slash", func(i *domains.Input) { i.Path = "api" }, "path"},
		{"path too long", func(i *domains.Input) { i.Path = "/" + strings.Repeat("a", 255) }, "path"},
		{"internal path with query", func(i *domains.Input) { i.InternalPath = "/a?b" }, "internalPath"},
		{"internal path with dollar", func(i *domains.Input) { i.InternalPath = "/a$b" }, "internalPath"},
		{"port zero", func(i *domains.Input) { i.Port = 0 }, "port"},
		{"port too high", func(i *domains.Input) { i.Port = 65536 }, "port"},
		{"entrypoint with space", func(i *domains.Input) { i.Entrypoint = "bad name" }, "entrypoint"},
		{"custom without resolver", func(i *domains.Input) { i.HTTPS, i.CertProvider, i.CertResolver = true, "custom", "" }, "certResolver"},
		{"unknown provider", func(i *domains.Input) { i.HTTPS, i.CertProvider = true, "acme" }, "certProvider"},
		{"comma middleware", func(i *domains.Input) { i.Middlewares = []string{"a,b"} }, "middlewares"},
		{"too many middlewares", func(i *domains.Input) { i.Middlewares = tooMany }, "middlewares"},
		{"unknown service", func(i *domains.Input) { i.Service = "db" }, "service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.mutate(&in)
			_, fe := domains.Validate(in, []string{"web"})
			if fe == nil {
				t.Fatal("Validate accepted the input")
			}
			if fe.Field != tc.field || fe.Message == "" {
				t.Errorf("error = %+v, want field %q with a message", fe, tc.field)
			}
		})
	}
}

func TestDeclaredServices(t *testing.T) {
	yamlContent := "services:\n  web: {image: a}\n  db: {image: b}\n  proxy: {image: c, network_mode: host}\n"
	if got, want := domains.DeclaredServices(yamlContent), []string{"db", "web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("services = %#v, want %#v", got, want)
	}
	for _, src := range []string{": :", ""} {
		got := domains.DeclaredServices(src)
		if got == nil || len(got) != 0 {
			t.Errorf("DeclaredServices(%q) = %#v, want empty non-nil", src, got)
		}
	}
}
