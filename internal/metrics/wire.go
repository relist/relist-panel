package metrics

import (
	"encoding/json"
	"math"
	"net/http"

	"relist-panel/internal/hostmetrics"
)

// The wire shapes mirror the design contract's MetricSample / ServiceSample
// (`web-design/src/data/types.ts`): epoch-millisecond `at`, camelCase metric
// fields, rates already computed, downsampling left to the chart.

// nullFloat is a metric value on the wire. JSON has no NaN, and a missing
// reading must never arrive disguised as a zero — so NaN marshals as null,
// which is what the charts read as "no point here" (and the handed-over
// decision from ticket 01: the wire encoding of NaN is null).
type nullFloat float64

func (f nullFloat) MarshalJSON() ([]byte, error) {
	if math.IsNaN(float64(f)) {
		return []byte("null"), nil
	}
	return json.Marshal(float64(f))
}

type hostSample struct {
	At          int64     `json:"at"` // epoch milliseconds
	CPU         nullFloat `json:"cpu"`
	MemUsed     nullFloat `json:"memUsed"`
	MemTotal    nullFloat `json:"memTotal"`
	DiskUsed    nullFloat `json:"diskUsed"`
	DiskTotal   nullFloat `json:"diskTotal"`
	DockerUsed  nullFloat `json:"dockerUsed"`
	DockerTotal nullFloat `json:"dockerTotal"`
	BlkRead     nullFloat `json:"blkRead"`
	BlkWrite    nullFloat `json:"blkWrite"`
	NetRx       nullFloat `json:"netRx"`
	NetTx       nullFloat `json:"netTx"`
}

type serviceSample struct {
	At       int64     `json:"at"`  // epoch milliseconds
	CPU      nullFloat `json:"cpu"` // percent of one core; can exceed 100
	MemUsed  nullFloat `json:"memUsed"`
	MemLimit nullFloat `json:"memLimit"`
	NetRx    nullFloat `json:"netRx"`
	NetTx    nullFloat `json:"netTx"`
	BlkRead  nullFloat `json:"blkRead"`
	BlkWrite nullFloat `json:"blkWrite"`
	Pids     nullFloat `json:"pids"`
}

type portMapping struct {
	HostIP        string `json:"hostIp"`
	HostPort      uint16 `json:"hostPort"`
	ContainerPort uint16 `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

type serviceFacts struct {
	Service       string        `json:"service"`
	ContainerID   string        `json:"containerId"`
	ContainerName string        `json:"containerName"`
	Image         string        `json:"image"`
	State         string        `json:"state"`
	Health        string        `json:"health"`
	Ports         []portMapping `json:"ports"`
}

// envelope is every metrics response's outer shape: which window, which store
// served it (`ring` or `rollups`), the samples' resolution, whether the data
// covers the window (`partial` — collecting, not complete: the honest display
// for a restart's gap), the sources availability block, and the samples.
type envelope struct {
	Window          string                              `json:"window"`
	From            string                              `json:"from"`
	IntervalSeconds int                                 `json:"intervalSeconds"`
	Partial         bool                                `json:"partial"`
	Sources         map[string]hostmetrics.SourceStatus `json:"sources"`
	Samples         any                                 `json:"samples"`
}

const (
	fromRing    = "ring"
	fromRollups = "rollups"
)

// wireSourceNames maps the sampler's internal source keys onto camelCase wire
// names, so the API reads like the rest of it. Sources that somehow miss the
// table pass through unchanged rather than disappearing.
var wireSourceNames = map[string]string{
	hostmetrics.SourceCPU:        "cpu",
	hostmetrics.SourceMemory:     "memory",
	hostmetrics.SourceDiskSpace:  "diskSpace",
	hostmetrics.SourceBlockIO:    "blockIo",
	hostmetrics.SourceNetwork:    "network",
	hostmetrics.SourceDockerDisk: "dockerDisk",
}

func wireSources(sources map[string]hostmetrics.SourceStatus) map[string]hostmetrics.SourceStatus {
	out := make(map[string]hostmetrics.SourceStatus, len(sources))
	for name, status := range sources {
		if key, ok := wireSourceNames[name]; ok {
			out[key] = status
		} else {
			out[name] = status
		}
	}
	return out
}

func hostSamplesWire(samples []hostmetrics.HostSample) []hostSample {
	out := make([]hostSample, len(samples))
	for i, sample := range samples {
		out[i] = hostSample{
			At:          sample.At.UnixMilli(),
			CPU:         nullFloat(sample.CPUPercent),
			MemUsed:     nullFloat(sample.MemUsedBytes),
			MemTotal:    nullFloat(sample.MemTotalBytes),
			DiskUsed:    nullFloat(sample.DiskTotalBytes - sample.DiskFreeBytes),
			DiskTotal:   nullFloat(sample.DiskTotalBytes),
			DockerUsed:  nullFloat(sample.DockerDiskBytes),
			DockerTotal: nullFloat(sample.DiskTotalBytes), // the device the data dir sits on
			BlkRead:     nullFloat(sample.DiskReadBPS),
			BlkWrite:    nullFloat(sample.DiskWriteBPS),
			NetRx:       nullFloat(sample.NetRXBPS),
			NetTx:       nullFloat(sample.NetTXBPS),
		}
	}
	return out
}

func serviceSamplesWire(samples []hostmetrics.ServiceSample) []serviceSample {
	out := make([]serviceSample, len(samples))
	for i, sample := range samples {
		out[i] = serviceSample{
			At:       sample.At.UnixMilli(),
			CPU:      nullFloat(sample.CPUPercent),
			MemUsed:  nullFloat(sample.MemUsedBytes),
			MemLimit: nullFloat(sample.MemLimitBytes),
			NetRx:    nullFloat(sample.NetRXBPS),
			NetTx:    nullFloat(sample.NetTXBPS),
			BlkRead:  nullFloat(sample.BlockReadBPS),
			BlkWrite: nullFloat(sample.BlockWriteBPS),
			Pids:     nullFloat(sample.Pids),
		}
	}
	return out
}

func portsWire(ports []hostmetrics.PortMapping) []portMapping {
	out := make([]portMapping, len(ports))
	for i, port := range ports {
		out[i] = portMapping{
			HostIP:        port.HostIP,
			HostPort:      port.HostPort,
			ContainerPort: port.ContainerPort,
			Protocol:      port.Protocol,
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
