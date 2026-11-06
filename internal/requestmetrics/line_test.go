package requestmetrics

import (
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	l, err := ParseLine([]byte(`{"StartUTC":"2026-11-06T10:00:01.5Z","RouterName":"relist-c12-d3@docker","DownstreamStatus":502,"Duration":1500000}`))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 6, 10, 0, 1, 500000000, time.UTC)
	if !l.Start.Equal(want) || l.Router != "relist-c12-d3@docker" || l.Status != 502 || l.Duration != 1500*time.Microsecond {
		t.Fatalf("line = %+v", l)
	}
	if _, err := ParseLine([]byte(`{"RouterName":"x"}`)); err == nil {
		t.Error("missing StartUTC must error")
	}
	if _, err := ParseLine([]byte(`not json`)); err == nil {
		t.Error("invalid JSON must error")
	}
	l, err = ParseLine([]byte(`{"StartUTC":"2026-11-06T10:00:01Z"}`))
	if err != nil || l.Router != "" {
		t.Errorf("no router = %+v, %v", l, err)
	}
}

func TestClassifyRouter(t *testing.T) {
	cases := []struct {
		name string
		want Route
	}{
		{"panel@docker", Route{Kind: RoutePanel}},
		{"panel-http@docker", Route{}},
		{"relist-c12-d3@docker", Route{Kind: RouteDomain, ComposeID: 12, DomainID: 3}},
		{"relist-c12-d3-http@docker", Route{}},
		{"relist-c12-d3", Route{Kind: RouteDomain, ComposeID: 12, DomainID: 3}},
		{"relist-c-d3@docker", Route{}},
		{"relist-c1-dx@docker", Route{}},
		{"myapp@docker", Route{}},
		{"", Route{}},
	}
	for _, c := range cases {
		if got := ClassifyRouter(c.name); got != c.want {
			t.Errorf("ClassifyRouter(%q) = %+v, want %+v", c.name, got, c.want)
		}
	}
}
