package migration

import (
	"context"
	"slices"
	"strings"
	"testing"

	"relist-panel/internal/deployments"
	"relist-panel/internal/domains"
)

func TestMaintenanceSkipsOffDomains(t *testing.T) {
	off := domains.Domain{ID: 4, ComposeID: 1, Host: "off.example.com", Path: "/", Port: 80, CertProvider: "none"}
	f := newSourceFixture(t, httpsDomain, off)
	if _, err := f.src.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for key, value := range f.docker.Maintenance {
		all.WriteString(key + "=" + value + "\n")
	}
	if !strings.Contains(all.String(), "shop.example.com") {
		t.Fatalf("maintenance labels lack the on domain:\n%s", all.String())
	}
	if strings.Contains(all.String(), "off.example.com") {
		t.Fatalf("maintenance labels cover an off domain:\n%s", all.String())
	}
}

func TestMaintenanceNotStartedWhenEveryDomainIsOff(t *testing.T) {
	off := httpsDomain
	off.Enabled = false
	f := newSourceFixture(t, off)
	if _, err := f.src.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if f.docker.MaintenanceUp {
		t.Fatal("maintenance container started with every domain off")
	}
}

func TestFinalizeDNSListsOffDomains(t *testing.T) {
	queue := &fakeEnqueuer{calls: map[int64]deployments.Mode{}}
	f := NewFinalizer(FinalizeDeps{Local: LocalHost{Docker: NewFakeDocker(), DataDir: t.TempDir()}, Queue: queue, Sources: sourceSet{},
		Domains: fakeDomains{{Host: "off.example.com"}}})
	summary, err := f.Complete(context.Background(), Manifest{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(summary.DNS, []string{"off.example.com"}) {
		t.Fatalf("DNS = %v, want the off domain's host", summary.DNS)
	}
}
