package requestmetrics

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Line is the part of one Access log line the metrics use.
type Line struct {
	Start    time.Time
	Router   string
	Status   int // DownstreamStatus: the status the client got
	Duration time.Duration
}

type rawLine struct {
	StartUTC         time.Time `json:"StartUTC"`
	RouterName       string    `json:"RouterName"`
	DownstreamStatus int       `json:"DownstreamStatus"`
	Duration         int64     `json:"Duration"`
}

// ParseLine reads one JSON Access log line. It fails on invalid JSON or a
// missing StartUTC.
func ParseLine(b []byte) (Line, error) {
	var r rawLine
	if err := json.Unmarshal(b, &r); err != nil {
		return Line{}, err
	}
	if r.StartUTC.IsZero() {
		return Line{}, errors.New("access log line has no StartUTC")
	}
	return Line{
		Start:    r.StartUTC.UTC(),
		Router:   r.RouterName,
		Status:   r.DownstreamStatus,
		Duration: time.Duration(r.Duration),
	}, nil
}

// RouteKind says which series a router's requests also count in.
type RouteKind int

const (
	RouteOther  RouteKind = iota // counts in the total only
	RoutePanel                   // the panel's own router
	RouteDomain                  // a Compose Domain's HTTPS router
)

// Route is a classified router name.
type Route struct {
	Kind      RouteKind
	ComposeID int64
	DomainID  int64
}

var domainRouter = regexp.MustCompile(`^relist-c(\d+)-d(\d+)$`)

// ClassifyRouter maps a Traefik router name to its Route. The panel writes
// these names, so they are trusted, unlike the request's Host header
// (ADR-0033). The -http redirect routers count in the total only.
func ClassifyRouter(name string) Route {
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	if name == "panel" {
		return Route{Kind: RoutePanel}
	}
	m := domainRouter.FindStringSubmatch(name)
	if m == nil {
		return Route{}
	}
	c, err1 := strconv.ParseInt(m[1], 10, 64)
	d, err2 := strconv.ParseInt(m[2], 10, 64)
	if err1 != nil || err2 != nil {
		return Route{}
	}
	return Route{Kind: RouteDomain, ComposeID: c, DomainID: d}
}
