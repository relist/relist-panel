// ContainerSource implements hostmetrics.ContainerSource by speaking HTTP
// over the Docker socket the panel already holds — ADR-0006's second way of
// talking to Docker, for the two endpoints the metrics loop needs
// (/containers/json, /containers/{id}/stats): the CLI path is measurably too
// expensive to poll, and the API returns raw numbers where the CLI's
// --format json returns formatted strings.
package dockerexec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"relist-panel/internal/hostmetrics"
)

// DefaultSocketPath is where the install mounts the host's Docker socket
// (ADR-0002).
const DefaultSocketPath = "/var/run/docker.sock"

// ContainerSource is the real hostmetrics.ContainerSource: an HTTP client
// dialling the daemon's unix socket.
type ContainerSource struct {
	client *http.Client
}

// NewContainerSource constructs a source over the socket at socketPath.
func NewContainerSource(socketPath string) *ContainerSource {
	return &ContainerSource{client: newSocketClient(socketPath)}
}

// newSocketClient is the HTTP client that dials the daemon's unix socket for
// every request, whatever the URL's host says. ADR-0006's direct Engine API
// path; the terminal exec adapter dials the same socket for everything except
// the hijacked start stream, which needs the raw connection.
func newSocketClient(socketPath string) *http.Client {
	dialer := &net.Dialer{}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// ListContainers reads GET /containers/json — running containers with their
// Compose labels, state, status text and ports, one request for the whole
// list (~0.01s, 16KB).
func (s *ContainerSource) ListContainers(ctx context.Context) ([]hostmetrics.ContainerInfo, error) {
	var apiContainers []apiContainer
	if err := s.get(ctx, "/containers/json", &apiContainers); err != nil {
		return nil, err
	}
	containers := make([]hostmetrics.ContainerInfo, 0, len(apiContainers))
	for _, api := range apiContainers {
		container := hostmetrics.ContainerInfo{
			ID:     api.ID,
			Image:  api.Image,
			State:  api.State,
			Status: api.Status,
			Labels: api.Labels,
		}
		if len(api.Names) > 0 {
			container.Name = strings.TrimPrefix(api.Names[0], "/")
		}
		for _, port := range api.Ports {
			container.Ports = append(container.Ports, hostmetrics.PortMapping{
				HostIP:        port.IP,
				HostPort:      port.PublicPort,
				ContainerPort: port.PrivatePort,
				Protocol:      port.Type,
			})
		}
		containers = append(containers, container)
	}
	return containers, nil
}

// ContainerStats reads GET /containers/{id}/stats?stream=false — one JSON
// object of cumulative counters (~1.9KB; the ~2s wall time is Docker
// sampling a delta window, not work).
func (s *ContainerSource) ContainerStats(ctx context.Context, id string) (hostmetrics.ContainerStatsSample, error) {
	path := "/containers/" + url.PathEscape(id) + "/stats?stream=false"
	var api apiStats
	if err := s.get(ctx, path, &api); err != nil {
		return hostmetrics.ContainerStatsSample{}, err
	}

	stats := hostmetrics.ContainerStatsSample{
		CPUTotalNanos:  api.CPUStats.CPUUsage.TotalUsage,
		CPUSystemNanos: api.CPUStats.SystemCPUUsage,
		CPUOnline:      api.CPUStats.OnlineCPUs,
		MemUsageBytes:  api.MemoryStats.Usage,
		MemLimitBytes:  api.MemoryStats.Limit,
		PidsCurrent:    api.PidsStats.PidsCurrent,
	}
	// cgroup v2 reports inactive_file where v1 reported cache; the daemon
	// decides which key is there and we follow, like the docker CLI does.
	if inactiveFile, ok := api.MemoryStats.Stats["inactive_file"]; ok {
		stats.MemCacheBytes = inactiveFile
	} else {
		stats.MemCacheBytes = api.MemoryStats.Stats["cache"]
	}
	for _, iface := range api.Networks {
		stats.NetRXBytes += iface.RXBytes
		stats.NetTXBytes += iface.TXBytes
	}
	for _, entry := range api.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			stats.BlockReadBytes += entry.Value
		case "write":
			stats.BlockWriteBytes += entry.Value
		}
	}
	return stats, nil
}

// SystemDf reads GET /system/df — Docker's own disk footprint, summed the
// way `docker system df` sums it: image layers (deduplicated by the daemon's
// content store), containers' writable layers (their root fs is already
// counted in the images), volumes, and build cache. ~0.67s wall per call,
// which is why the sampler refreshes it every few minutes and carries it
// forward between refreshes.
func (s *ContainerSource) SystemDf(ctx context.Context) (uint64, error) {
	var api apiSystemDf
	if err := s.get(ctx, "/system/df", &api); err != nil {
		return 0, err
	}
	total := api.LayersSize
	for _, container := range api.Containers {
		total += container.SizeRw
	}
	for _, volume := range api.Volumes {
		total += volume.UsageData.Size
	}
	for _, record := range api.BuildCache {
		total += record.Size
	}
	return total, nil
}

func (s *ContainerSource) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return fmt.Errorf("docker api %s: %w", path, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("docker api %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker api %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("docker api %s: decode: %w", path, err)
	}
	return nil
}

// apiSystemDf is GET /system/df's object shape, reduced to the four sizes
// the aggregate sums.
type apiSystemDf struct {
	LayersSize uint64 `json:"LayersSize"`
	Containers []struct {
		SizeRw uint64 `json:"SizeRw"`
	} `json:"Containers"`
	Volumes []struct {
		UsageData struct {
			Size uint64 `json:"Size"`
		} `json:"UsageData"`
	} `json:"Volumes"`
	BuildCache []struct {
		Size uint64 `json:"Size"`
	} `json:"BuildCache"`
}

// apiContainer is GET /containers/json's row shape.
type apiContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Ports  []apiPort         `json:"Ports"`
	Labels map[string]string `json:"Labels"`
}

type apiPort struct {
	IP          string `json:"IP"`
	PrivatePort uint16 `json:"PrivatePort"`
	PublicPort  uint16 `json:"PublicPort"`
	Type        string `json:"Type"`
}

// apiStats is GET /containers/{id}/stats?stream=false's object shape — raw
// numbers only: nanoseconds, bytes, counts.
type apiStats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint64 `json:"online_cpus"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RXBytes uint64 `json:"rx_bytes"`
		TXBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	PidsStats struct {
		PidsCurrent uint64 `json:"current"`
	} `json:"pids_stats"`
	BlkioStats struct {
		IoServiceBytesRecursive []struct {
			Major uint64 `json:"major"`
			Minor uint64 `json:"minor"`
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
}
