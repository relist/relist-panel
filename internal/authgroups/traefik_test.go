package authgroups

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type fileDoc struct {
	HTTP struct {
		Middlewares map[string]struct {
			BasicAuth struct {
				Realm        string   `yaml:"realm"`
				RemoveHeader bool     `yaml:"removeHeader"`
				Users        []string `yaml:"users"`
			} `yaml:"basicAuth"`
		} `yaml:"middlewares"`
	} `yaml:"http"`
}

func readDoc(t *testing.T, dir string, id int64) (fileDoc, os.FileInfo) {
	t.Helper()
	path := filepath.Join(dir, fileName(id))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "# Written by relist-panel from the auth_groups table. Edits are overwritten.\n") {
		t.Errorf("missing header:\n%s", body)
	}
	var doc fileDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("yaml: %v\n%s", err, body)
	}
	info, _ := os.Stat(path)
	return doc, info
}

func hashed(t *testing.T, pw string) string {
	t.Helper()
	h, err := hashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestWriteDirFile(t *testing.T) {
	dir := t.TempDir()
	pw := `pa$$ "w#rd x`
	groups := []GroupUsers{{
		Group: Group{ID: 1, Name: "Staging"},
		Users: []User{{Username: "alice", Hash: hashed(t, pw)}, {Username: "bob", Hash: hashed(t, "another-pass")}},
	}}
	if err := WriteDir(dir, groups); err != nil {
		t.Fatal(err)
	}
	doc, _ := readDoc(t, dir, 1)
	mw, ok := doc.HTTP.Middlewares["relist-auth-1"]
	if !ok {
		t.Fatalf("middlewares = %+v", doc.HTTP.Middlewares)
	}
	if mw.BasicAuth.Realm != "Staging" || !mw.BasicAuth.RemoveHeader || len(mw.BasicAuth.Users) != 2 {
		t.Fatalf("basicAuth = %+v", mw.BasicAuth)
	}
	user, hash, _ := strings.Cut(mw.BasicAuth.Users[0], ":")
	if user != "alice" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		t.Errorf("alice entry = %q", mw.BasicAuth.Users[0])
	}
	if !strings.HasPrefix(mw.BasicAuth.Users[1], "bob:") {
		t.Errorf("second entry = %q", mw.BasicAuth.Users[1])
	}
}

func TestWriteDirYAMLSpecialName(t *testing.T) {
	dir := t.TempDir()
	name := `a"b: c #x`
	if err := WriteDir(dir, []GroupUsers{{Group: Group{ID: 2, Name: name}, Users: []User{{Username: "u", Hash: hashed(t, "password-1")}}}}); err != nil {
		t.Fatal(err)
	}
	doc, _ := readDoc(t, dir, 2)
	if got := doc.HTTP.Middlewares["relist-auth-2"].BasicAuth.Realm; got != name {
		t.Errorf("realm = %q", got)
	}
}

func TestWriteDirEmptyGroupLocked(t *testing.T) {
	dir := t.TempDir()
	groups := []GroupUsers{{Group: Group{ID: 3, Name: "Empty"}}}
	var names []string
	for i := 0; i < 2; i++ {
		if err := WriteDir(dir, groups); err != nil {
			t.Fatal(err)
		}
		doc, _ := readDoc(t, dir, 3)
		users := doc.HTTP.Middlewares["relist-auth-3"].BasicAuth.Users
		if len(users) != 1 {
			t.Fatalf("users = %v", users)
		}
		user, _, _ := strings.Cut(users[0], ":")
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(user) {
			t.Errorf("username = %q", user)
		}
		names = append(names, user)
	}
	if names[0] == names[1] {
		t.Error("locked user is the same on every write")
	}
}

func TestWriteDirRemovesStaleOnly(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"auth-99.yml", "cert-1.yml", "mine.yml"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteDir(dir, []GroupUsers{{Group: Group{ID: 1, Name: "g"}, Users: []User{{Username: "u", Hash: hashed(t, "password-1")}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth-99.yml")); !os.IsNotExist(err) {
		t.Error("stale auth-99.yml kept")
	}
	for _, n := range []string{"cert-1.yml", "mine.yml"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s removed: %v", n, err)
		}
	}
	_, info := readDoc(t, dir, 1)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	tmps, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	hidden, _ := filepath.Glob(filepath.Join(dir, ".*.tmp"))
	if len(tmps)+len(hidden) != 0 {
		t.Errorf("temp files left: %v %v", tmps, hidden)
	}
}

func TestReconcileCreatesMissingDirAndRenamesUser(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	dataDir := t.TempDir()
	g, err := s.Create(ctx, "Staging", []UserInput{{"alice", "password-one"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(ctx, s, dataDir); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dataDir, ".traefik", "dynamic")
	doc, _ := readDoc(t, dir, g.ID)
	before := doc.HTTP.Middlewares[MiddlewareName(g.ID)].BasicAuth.Users[0]
	_, hashBefore, _ := strings.Cut(before, ":")

	users, _ := s.Users(ctx, g.ID)
	al := "al"
	if _, err := s.UpdateUser(ctx, g.ID, users[0].ID, &al, nil); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(ctx, s, dataDir); err != nil {
		t.Fatal(err)
	}
	doc, _ = readDoc(t, dir, g.ID)
	after := doc.HTTP.Middlewares[MiddlewareName(g.ID)].BasicAuth.Users[0]
	user, hashAfter, _ := strings.Cut(after, ":")
	if user != "al" || hashAfter != hashBefore {
		t.Errorf("after rename = %q (hash kept: %v)", after, hashAfter == hashBefore)
	}
	if bcrypt.CompareHashAndPassword([]byte(hashAfter), []byte("password-one")) != nil {
		t.Error("hash no longer verifies")
	}
}
