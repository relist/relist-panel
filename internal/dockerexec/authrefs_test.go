package dockerexec

import (
	"reflect"
	"testing"
)

func TestParseAuthRefs(t *testing.T) {
	out := "/relist-c1-web-1\ttraefik.enable=true\ttraefik.http.routers.relist-c1-d1.middlewares=relist-auth-3@file,relist-c1-d1-strip\n" +
		"/relist-c2-api-1\ttraefik.http.routers.relist-c2-d4.middlewares=relist-auth-30@file\n" +
		"/hand-written\ttraefik.http.routers.mine.middlewares= relist-auth-3@file \tother=relist-auth-3@file\n" +
		"/label-elsewhere\tnote=relist-auth-3@file\n" +
		"\n"
	got := parseAuthRefs(out, "relist-auth-3@file")
	want := []string{"relist-c1-web-1", "hand-written"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseAuthRefs = %v, want %v", got, want)
	}
	if got := parseAuthRefs("", "relist-auth-3@file"); len(got) != 0 {
		t.Errorf("empty output = %v", got)
	}
}
