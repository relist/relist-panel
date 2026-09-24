package hostmetrics

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

func TestParseCPUStatGolden(t *testing.T) {
	times, err := ParseCPUStat(loadFixture(t, "stat.golden"))
	if err != nil {
		t.Fatalf("ParseCPUStat() error = %v", err)
	}
	// user 100 + nice 200 + system 300 + irq 60 = 660 busy; idle 4000 +
	// iowait 500 count as idle. guest 70 and guest_nice 80 must be left out
	// of the total: the kernel already counts them inside user and nice.
	if times.Busy != 660 {
		t.Errorf("Busy = %d, want 660", times.Busy)
	}
	if times.Total != 5160 {
		t.Errorf("Total = %d, want 5160 (guest jiffies excluded)", times.Total)
	}
}

func TestParseCPUStatFreshlyBooted(t *testing.T) {
	// A just-booted host: tiny counters, a short file, no per-CPU lines yet.
	times, err := ParseCPUStat("cpu  12 0 8 240 0 0 0 0 0 0\n")
	if err != nil {
		t.Fatalf("ParseCPUStat() error = %v", err)
	}
	if times.Busy != 20 || times.Total != 260 {
		t.Errorf("Busy/Total = %d/%d, want 20/260", times.Busy, times.Total)
	}
}

func TestParseCPUStatErrors(t *testing.T) {
	for name, text := range map[string]string{
		"no cpu line":   "intr 1 2 3\nctxt 5\n",
		"short pc line": "cpu  1 2 3\n",
		"garbage":       "cpu  a b c d e f g h\n",
		"empty":         "",
	} {
		if _, err := ParseCPUStat(text); err == nil {
			t.Errorf("%s: ParseCPUStat() = nil error, want error (parsers error rather than return zeros)", name)
		}
	}
}

func TestParseMemInfoGolden(t *testing.T) {
	info, err := ParseMemInfo(loadFixture(t, "meminfo.golden"))
	if err != nil {
		t.Fatalf("ParseMemInfo() error = %v", err)
	}
	if info.TotalBytes != 16384*1024 {
		t.Errorf("TotalBytes = %d, want %d", info.TotalBytes, 16384*1024)
	}
	// MemAvailable (8192 kB), not MemFree (2048 kB): availability is what
	// makes the panel's used number agree with free and htop.
	if info.AvailableBytes != 8192*1024 {
		t.Errorf("AvailableBytes = %d, want %d", info.AvailableBytes, 8192*1024)
	}
}

func TestParseMemInfoErrors(t *testing.T) {
	for name, text := range map[string]string{
		"missing MemAvailable": "MemTotal: 100 kB\nMemFree: 50 kB\n",
		"missing MemTotal":     "MemAvailable: 50 kB\n",
		"empty":                "",
	} {
		if _, err := ParseMemInfo(text); err == nil {
			t.Errorf("%s: ParseMemInfo() = nil error, want error", name)
		}
	}
}

func TestParseDiskStatsGolden(t *testing.T) {
	devices, err := ParseDiskStats(loadFixture(t, "diskstats.golden"))
	if err != nil {
		t.Fatalf("ParseDiskStats() error = %v", err)
	}
	// Whole physical disks only: sda, sdaa, nvme0n1, mmcblk0, vdb, hda.
	// Excluded: the partitions under them (sda1, nvme0n1p1, mmcblk0p1),
	// loop/ram/zram, and the dm layer on top — each of those re-reports the
	// same sectors, and summing them would multiply the rate.
	want := []DiskCounters{
		{Device: "sda", ReadBytes: 123456 * 512, WriteBytes: 654321 * 512},
		{Device: "sdaa", ReadBytes: 60 * 512, WriteBytes: 90 * 512},
		{Device: "nvme0n1", ReadBytes: 20000 * 512, WriteBytes: 40000 * 512},
		{Device: "mmcblk0", ReadBytes: 1000 * 512, WriteBytes: 400 * 512},
		{Device: "vdb", ReadBytes: 88 * 512, WriteBytes: 77 * 512},
		{Device: "hda", ReadBytes: 222 * 512, WriteBytes: 55 * 512},
	}
	if !reflect.DeepEqual(devices, want) {
		t.Errorf("ParseDiskStats() = %+v, want %+v", devices, want)
	}
}

func TestParseDiskStatsErrors(t *testing.T) {
	for name, text := range map[string]string{
		"no whole disk": "   7       0 loop0 9 0 9 0 0 0 0 0 0 0 0 0 0 0\n" +
			" 254       0 zram0 5 0 5 0 5 0 5 0 0 0 0 0 0 0\n" +
			" 252       0 dm-0 11 0 22 0 33 0 44 0 0 0 0 0 0 0\n" +
			"   8       1 sda1 100 0 200 10 30 0 40 5 0 0 0 0 0 0\n",
		"empty": "",
	} {
		if _, err := ParseDiskStats(text); err == nil {
			t.Errorf("%s: ParseDiskStats() = nil error, want error", name)
		}
	}
}

func TestParseNetDevGolden(t *testing.T) {
	ifaces, err := ParseNetDev(loadFixture(t, "netdev.golden"))
	if err != nil {
		t.Fatalf("ParseNetDev() error = %v", err)
	}
	// lo excluded: its counters are the host's own local traffic, and
	// counting it would double every loopback byte.
	want := []NetCounters{
		{Device: "eth0", RXBytes: 5273301899, TXBytes: 698000000},
		{Device: "eth1", RXBytes: 1000, TXBytes: 2000},
		{Device: "docker0", RXBytes: 5000, TXBytes: 6000},
	}
	if !reflect.DeepEqual(ifaces, want) {
		t.Errorf("ParseNetDev() = %+v, want %+v", ifaces, want)
	}
}

func TestParseNetDevErrors(t *testing.T) {
	for name, text := range map[string]string{
		"loopback only": "Inter-|   Receive  |  Transmit\n face |bytes packets|bytes packets\n    lo:  200 1 0 0 0 0 0 0   200 1 0 0 0 0 0 0\n",
		"headers only":  "Inter-|   Receive  |  Transmit\n face |bytes packets|bytes packets\n",
		"empty":         "",
	} {
		if _, err := ParseNetDev(text); err == nil {
			t.Errorf("%s: ParseNetDev() = nil error, want error", name)
		}
	}
}
