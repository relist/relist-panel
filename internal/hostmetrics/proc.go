package hostmetrics

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// CPUTimes is a cumulative CPU counter pair from /proc/stat: jiffies spent
// busy and the total the kernel accounts for.
type CPUTimes struct {
	Busy  uint64
	Total uint64
}

// ParseCPUStat reads /proc/stat's aggregate cpu line into busy/total jiffies.
// idle and iowait count as idle; guest and guest_nice are left out of the
// total — the kernel already counts them inside user and nice. It errors
// rather than returning zeros when nothing matched, so "cannot read this
// source" stays distinguishable from "the host is idle".
func ParseCPUStat(text string) (CPUTimes, error) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "cpu" { // skips cpu0, cpu1, ...
			continue
		}
		if len(fields)-1 < 8 {
			return CPUTimes{}, fmt.Errorf("aggregate cpu line has %d fields, want at least 8", len(fields)-1)
		}
		values := make([]uint64, len(fields)-1)
		for i, field := range fields[1:] {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return CPUTimes{}, fmt.Errorf("parse cpu time %q: %w", field, err)
			}
			values[i] = value
		}
		// user, nice, system, idle, iowait, irq, softirq, steal; guest and
		// guest_nice follow and are deliberately excluded.
		var total uint64
		for _, value := range values[:8] {
			total += value
		}
		idle := values[3] + values[4]
		return CPUTimes{Busy: total - idle, Total: total}, nil
	}
	return CPUTimes{}, errors.New("no aggregate cpu line found")
}

// MemInfo is the host's memory in bytes.
type MemInfo struct {
	TotalBytes     uint64
	AvailableBytes uint64
}

// ParseMemInfo reads MemTotal and MemAvailable from /proc/meminfo, the pair
// used = total − available that agrees with free and htop. It errors when
// either key is missing.
func ParseMemInfo(text string) (MemInfo, error) {
	var (
		info                     MemInfo
		haveTotal, haveAvailable bool
	)
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			value, err := parseKB(fields[1])
			if err != nil {
				return MemInfo{}, fmt.Errorf("parse MemTotal: %w", err)
			}
			info.TotalBytes, haveTotal = value, true
		case "MemAvailable":
			value, err := parseKB(fields[1])
			if err != nil {
				return MemInfo{}, fmt.Errorf("parse MemAvailable: %w", err)
			}
			info.AvailableBytes, haveAvailable = value, true
		}
	}
	if !haveTotal || !haveAvailable {
		return MemInfo{}, errors.New("MemTotal or MemAvailable missing")
	}
	return info, nil
}

// parseKB reads a /proc/meminfo value, which the kernel reports in kB
// (kilobytes that are actually kibibytes), and returns bytes.
func parseKB(field string) (uint64, error) {
	kb, err := strconv.ParseUint(field, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", field, err)
	}
	return kb * 1024, nil
}

// DiskCounters is one whole physical device's cumulative block I/O in bytes —
// the kernel's sector counts times the fixed 512-byte sector size, whatever
// the device's logical sector size is.
type DiskCounters struct {
	Device     string
	ReadBytes  uint64
	WriteBytes uint64
}

// ParseDiskStats extracts the whole physical devices from /proc/diskstats:
// sdX, vdX, hdX, nvmeXnY, mmcblkX, and never their partitions — a partition
// and the disk under it (and the dm layers above) each report the same bytes,
// and summing them all would multiply the rate. loop, ram, zram and
// device-mapper devices are skipped. It errors when no whole disk matched.
func ParseDiskStats(text string) ([]DiskCounters, error) {
	var devices []DiskCounters
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		name := fields[2]
		if !wholeDiskDevice(name) {
			continue
		}
		readSectors, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s read sectors %q: %w", name, fields[5], err)
		}
		writtenSectors, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s write sectors %q: %w", name, fields[9], err)
		}
		devices = append(devices, DiskCounters{
			Device:     name,
			ReadBytes:  readSectors * 512,
			WriteBytes: writtenSectors * 512,
		})
	}
	if len(devices) == 0 {
		return nil, errors.New("no whole-disk device found")
	}
	return devices, nil
}

// wholeDiskDevice reports whether a /proc/diskstats name is a whole physical
// disk rather than one of its partitions or a virtual layer: sdX/vdX/hdX are
// letters only (sda, sdaa), nvme is nvme<controller>n<namespace> (partitions
// append p<n>), mmcblk is mmcblk<n> (partitions append p<n>).
func wholeDiskDevice(name string) bool {
	for _, prefix := range []string{"sd", "vd", "hd"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok && allLetters(rest) {
			return true
		}
	}
	if rest, ok := strings.CutPrefix(name, "nvme"); ok {
		controller, after, found := strings.Cut(rest, "n")
		if !found || controller == "" || !allDigits(controller) {
			return false
		}
		if _, _, isPartition := strings.Cut(after, "p"); isPartition {
			return false
		}
		return after != "" && allDigits(after)
	}
	if rest, ok := strings.CutPrefix(name, "mmcblk"); ok {
		return rest != "" && allDigits(rest)
	}
	return false
}

// NetCounters is one network interface's cumulative byte counters.
type NetCounters struct {
	Device  string
	RXBytes uint64
	TXBytes uint64
}

// ParseNetDev reads /proc/net/dev (the host's, via PID 1's namespace —
// ADR-0006), skipping loopback so local traffic is not counted twice, and
// Docker's bridges and veths, which relay a container's traffic that the
// host's real interface already counted: a container's download is eth0 rx,
// then bridge tx and veth tx again. It errors when no other interface
// matched.
func ParseNetDev(text string) ([]NetCounters, error) {
	var interfaces []NetCounters
	for _, line := range strings.Split(text, "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found { // the two header lines
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || name == "lo" || isDockerInterface(name) {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 16 { // 8 receive fields, 8 transmit fields
			continue
		}
		rx, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s rx bytes %q: %w", name, fields[0], err)
		}
		tx, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s tx bytes %q: %w", name, fields[8], err)
		}
		interfaces = append(interfaces, NetCounters{Device: name, RXBytes: rx, TXBytes: tx})
	}
	if len(interfaces) == 0 {
		return nil, errors.New("no interface found besides loopback and Docker's")
	}
	return interfaces, nil
}

// isDockerInterface reports whether name is one Docker creates on the host:
// the default bridge, the swarm gateway bridge, a user network's bridge
// (br-<network id>) or a container's veth end.
func isDockerInterface(name string) bool {
	return name == "docker0" || name == "docker_gwbridge" ||
		strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth")
}

func allLetters(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return s != ""
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
