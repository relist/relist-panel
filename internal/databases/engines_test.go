package databases

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	if _, err := Lookup("postgres", "18"); err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup("postgres", "9"); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("err = %v, want ErrUnknownVersion", err)
	}
	if _, err := Lookup("oracle", "1"); !errors.Is(err, ErrUnknownEngine) {
		t.Fatalf("err = %v, want ErrUnknownEngine", err)
	}
	for name, e := range Engines {
		if e.Name() != name {
			t.Errorf("%s registered as %s", e.Name(), name)
		}
		defaults := 0
		for _, v := range e.Versions() {
			if v.Default {
				defaults++
			}
		}
		if defaults != 1 {
			t.Errorf("%s has %d default versions", name, defaults)
		}
	}
	if len(Engines) != 4 {
		t.Fatalf("engines = %d", len(Engines))
	}
}

func TestPostgresDataPath(t *testing.T) {
	pg := Engines["postgres"]
	for tag, want := range map[string]string{
		"18": "/var/lib/postgresql", "17": "/var/lib/postgresql/data", "16": "/var/lib/postgresql/data",
	} {
		if got := pg.DataPath(tag); got != want {
			t.Errorf("DataPath(%s) = %s, want %s", tag, got, want)
		}
	}
}

func TestURLEncodesCredentials(t *testing.T) {
	const pw = "a-b_c/d@e:f%"
	for name, e := range Engines {
		raw := e.URL("relist-db-3", pw, e.DefaultDatabase())
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: parse %q: %v", name, raw, err)
		}
		got, _ := u.User.Password()
		if got != pw || u.Hostname() != "relist-db-3" || u.Port() != strconv.Itoa(e.Port()) {
			t.Errorf("%s: %q parsed to pw=%q host=%q port=%q", name, raw, got, u.Hostname(), u.Port())
		}
	}
	if got := Engines["redis"].URL("h", "p", "0"); got != "redis://:p@h:6379/0" {
		t.Errorf("redis url = %s", got)
	}
	if got := Engines["mysql"].URL("h", "p", ""); got != "mysql://root:p@h:3306" {
		t.Errorf("mysql url = %s", got)
	}
}

func TestRedisKeepsPasswordOutOfCmd(t *testing.T) {
	e := Engines["redis"]
	const pw = "s3cret-pw"
	env := strings.Join(e.Env(pw), " ")
	if !strings.Contains(env, pw) {
		t.Fatal("env lacks the password")
	}
	for _, a := range e.Cmd() {
		if strings.Contains(a, pw) {
			t.Fatalf("Cmd contains the password: %q", a)
		}
	}
}

func TestImage(t *testing.T) {
	if got := Engines["mariadb"].Image("11.8"); got != "mariadb:11.8" {
		t.Fatalf("Image = %s", got)
	}
}
