package dockerexec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"relist-panel/internal/dockerhost"
)

// DockerHost reads and prunes only the connected host through the Engine API.
// Containers, networks and build cache use Docker's own prune endpoints with
// fixed options. Volumes and images are listed, then removed one by one with an
// unforced delete, so the caller decides which ones go. No arbitrary command,
// path or filter, and no force-delete option, reaches Docker.
type DockerHost struct{ source *ContainerSource }

func NewDockerHost(socketPath string) *DockerHost {
	return &DockerHost{source: NewContainerSource(socketPath)}
}

var _ dockerhost.Engine = (*DockerHost)(nil)

type daemonVersion struct {
	Version    string
	APIVersion string `json:"ApiVersion"`
}

func (d *DockerHost) version(ctx context.Context) (daemonVersion, error) {
	var v daemonVersion
	err := d.source.get(ctx, "/version", &v)
	return v, err
}
func bytesAvailable(value int64) *uint64 {
	if value < 0 {
		return nil
	}
	result := uint64(value)
	return &result
}
func (d *DockerHost) Report(ctx context.Context) (dockerhost.Report, error) {
	v, err := d.version(ctx)
	if err != nil {
		return dockerhost.Report{}, err
	}
	var info struct {
		Name, Driver, DockerRootDir                            string
		ContainersRunning, ContainersStopped, ContainersPaused int
	}
	if err := d.source.get(ctx, "/info", &info); err != nil {
		return dockerhost.Report{}, err
	}
	var df struct {
		LayersSize int64
		Images     []struct{ Containers int64 }
		Containers []struct {
			ID           string `json:"Id"`
			Names        []string
			Image, State string
			SizeRw       int64
		}
		Volumes []struct {
			Name, Driver string
			UsageData    *struct{ Size, RefCount int64 }
		}
		BuildCache []struct {
			Size          int64
			InUse, Shared bool
		}
	}
	if err := d.source.get(ctx, "/system/df", &df); err != nil {
		return dockerhost.Report{}, err
	}
	// /system/df omits plugin volumes and local volumes with mount options.
	// Inventory comes from /volumes; usage is an optional join by stable name.
	var inventory struct {
		Volumes []struct{ Name, Driver string }
	}
	if err := d.source.get(ctx, "/volumes", &inventory); err != nil {
		return dockerhost.Report{}, err
	}
	var networks []struct {
		ID                  string `json:"Id"`
		Name, Driver, Scope string
		Containers          map[string]json.RawMessage
	}
	if err := d.source.get(ctx, "/networks", &networks); err != nil {
		return dockerhost.Report{}, err
	}
	report := dockerhost.Report{
		Version: v.Version, APIVersion: v.APIVersion, Hostname: info.Name, StorageDriver: info.Driver, DockerRootDir: info.DockerRootDir,
		ContainersRunning: info.ContainersRunning, ContainersStopped: info.ContainersStopped, ContainersPaused: info.ContainersPaused,
		Containers: []dockerhost.Container{}, Volumes: []dockerhost.Volume{}, Networks: []dockerhost.Network{}, Warnings: []string{},
	}
	imageUsage := dockerhost.Usage{Kind: "images", Count: len(df.Images), Size: bytesAvailable(df.LayersSize)}
	for _, image := range df.Images {
		if image.Containers > 0 {
			imageUsage.Active++
		}
	}
	// The API supplies deduplicated LayersSize, but no deduplicated reclaimable
	// image total. Summing image sizes would double-count shared layers.
	var containerSize, containerFree int64
	for _, c := range df.Containers {
		names := c.Names
		if names == nil {
			names = []string{}
		}
		report.Containers = append(report.Containers, dockerhost.Container{ID: c.ID, Names: names, Image: c.Image, State: c.State, Size: bytesAvailable(c.SizeRw)})
		if c.SizeRw < 0 {
			containerSize = -1
			containerFree = -1
			continue
		}
		if containerSize >= 0 {
			containerSize += c.SizeRw
		}
		if containerFree >= 0 && (c.State == "exited" || c.State == "created" || c.State == "dead") {
			containerFree += c.SizeRw
		}
	}
	containerUsage := dockerhost.Usage{Kind: "containers", Count: len(df.Containers), Active: info.ContainersRunning + info.ContainersPaused, Size: bytesAvailable(containerSize), Reclaimable: bytesAvailable(containerFree)}
	var volumeSize, volumeFree int64
	volumeUsage := dockerhost.Usage{Kind: "volumes", Count: len(inventory.Volumes)}
	usageByName := make(map[string]*struct{ Size, RefCount int64 }, len(df.Volumes))
	for _, v := range df.Volumes {
		usageByName[v.Name] = v.UsageData
	}
	for _, v := range inventory.Volumes {
		usage := usageByName[v.Name]
		row := dockerhost.Volume{Name: v.Name, Driver: v.Driver}
		if usage != nil {
			row.Size = bytesAvailable(usage.Size)
			if usage.RefCount >= 0 {
				n := usage.RefCount
				row.References = &n
			}
		}
		report.Volumes = append(report.Volumes, row)
		if row.Size == nil {
			volumeSize = -1
		} else if volumeSize >= 0 {
			volumeSize += int64(*row.Size)
		}
		if row.References == nil {
			volumeUsage.Active = -1
			volumeFree = -1
			continue
		}
		if *row.References > 0 && volumeUsage.Active >= 0 {
			volumeUsage.Active++
		}
		if *row.References == 0 {
			if row.Size == nil {
				volumeFree = -1
			} else if volumeFree >= 0 {
				volumeFree += int64(*row.Size)
			}
		}
	}
	volumeUsage.Size = bytesAvailable(volumeSize)
	volumeUsage.Reclaimable = bytesAvailable(volumeFree)
	if volumeSize < 0 || volumeFree < 0 {
		report.Warnings = append(report.Warnings, "Some volume drivers do not report usage. Their sizes and reclaimable total are unavailable.")
	}
	var cacheSize, cacheFree int64
	cacheUsage := dockerhost.Usage{Kind: "build-cache", Count: len(df.BuildCache)}
	for _, c := range df.BuildCache {
		if c.InUse {
			cacheUsage.Active++
		}
		if c.Size < 0 {
			cacheSize = -1
			cacheFree = -1
			continue
		}
		if cacheSize >= 0 {
			cacheSize += c.Size
		}
		if !c.InUse && !c.Shared && cacheFree >= 0 {
			cacheFree += c.Size
		}
	}
	cacheUsage.Size = bytesAvailable(cacheSize)
	cacheUsage.Reclaimable = bytesAvailable(cacheFree)
	networkUsage := dockerhost.Usage{Kind: "networks", Count: len(networks), Active: -1}
	for _, n := range networks {
		var count *int
		if n.Containers != nil {
			v := len(n.Containers)
			count = &v
		}
		report.Networks = append(report.Networks, dockerhost.Network{ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Containers: count})
	}
	report.Usage = []dockerhost.Usage{imageUsage, containerUsage, volumeUsage, cacheUsage, networkUsage}
	return report, nil
}

var apiVersionPattern = regexp.MustCompile(`^1\.[0-9]+$`)

func (d *DockerHost) Prune(ctx context.Context, kind dockerhost.Kind) (dockerhost.PruneResult, error) {
	result := dockerhost.PruneResult{Kind: kind, Deleted: []string{}}
	if kind.Confirmation() == "" {
		return result, fmt.Errorf("unknown Docker prune operation")
	}
	if kind.Listed() {
		return result, fmt.Errorf("%s must be listed and removed one by one, not pruned host-wide", kind)
	}
	v, err := d.version(ctx)
	if err != nil {
		return result, err
	}
	if !apiVersionPattern.MatchString(v.APIVersion) {
		return result, fmt.Errorf("Docker reported an invalid API version")
	}
	minor, _ := strconv.Atoi(strings.TrimPrefix(v.APIVersion, "1."))
	if minor < 25 {
		return result, fmt.Errorf("this prune operation requires a newer Docker Engine API")
	}
	path := ""
	query := url.Values{}
	switch kind {
	case dockerhost.Containers:
		path = "containers"
	case dockerhost.Networks:
		path = "networks"
	case dockerhost.BuildCache:
		path = "build"
		query.Set("all", "true")
	}
	endpoint := "/v" + v.APIVersion + "/" + path + "/prune"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker"+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return result, err
	}
	resp, err := d.source.client.Do(req)
	if err != nil {
		return result, fmt.Errorf("%w: %v", dockerhost.ErrPruneUncertain, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var body struct{ Message string }
		if json.Unmarshal(raw, &body) == nil && body.Message != "" {
			return result, fmt.Errorf("Docker prune: %s", body.Message)
		}
		return result, fmt.Errorf("Docker prune: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var body struct {
		SpaceReclaimed                                    uint64
		ContainersDeleted, NetworksDeleted, CachesDeleted []string
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return result, fmt.Errorf("%w: could not read the result: %v", dockerhost.ErrPruneUncertain, err)
	}
	result.SpaceReclaimed = body.SpaceReclaimed
	switch kind {
	case dockerhost.Containers:
		result.Deleted = append(result.Deleted, body.ContainersDeleted...)
	case dockerhost.Networks:
		result.Deleted = append(result.Deleted, body.NetworksDeleted...)
	case dockerhost.BuildCache:
		result.Deleted = append(result.Deleted, body.CachesDeleted...)
	}
	return result, nil
}

// anonymousVolumeLabel marks a volume Docker created for a container's
// anonymous VOLUME, which Docker's default volume prune removes.
const anonymousVolumeLabel = "com.docker.volume.anonymous"

func (d *DockerHost) Candidates(ctx context.Context, kind dockerhost.Kind) ([]dockerhost.Candidate, error) {
	switch kind {
	case dockerhost.Volumes, dockerhost.AllVolumes:
		return d.volumeCandidates(ctx, kind)
	case dockerhost.Images, dockerhost.UnusedImages:
		return d.imageCandidates(ctx, kind)
	}
	return nil, fmt.Errorf("%s has no candidate list", kind)
}

func (d *DockerHost) volumeCandidates(ctx context.Context, kind dockerhost.Kind) ([]dockerhost.Candidate, error) {
	var listing struct {
		Volumes []struct {
			Name      string
			Labels    map[string]string
			UsageData *struct{ Size int64 }
		}
	}
	query := url.Values{"filters": {`{"dangling":["true"]}`}}
	if err := d.source.get(ctx, "/volumes?"+query.Encode(), &listing); err != nil {
		return nil, err
	}
	// Sizes are optional: only /system/df has them, so a failure costs the
	// sizes and nothing else.
	var df struct {
		Volumes []struct {
			Name      string
			UsageData *struct{ Size int64 }
		}
	}
	sizes := map[string]*uint64{}
	if d.source.get(ctx, "/system/df", &df) == nil {
		for _, v := range df.Volumes {
			if v.UsageData != nil {
				sizes[v.Name] = bytesAvailable(v.UsageData.Size)
			}
		}
	}
	candidates := []dockerhost.Candidate{}
	for _, v := range listing.Volumes {
		if _, anonymous := v.Labels[anonymousVolumeLabel]; kind == dockerhost.Volumes && !anonymous {
			continue
		}
		size := sizes[v.Name]
		if v.UsageData != nil {
			size = bytesAvailable(v.UsageData.Size)
		}
		candidates = append(candidates, dockerhost.Candidate{ID: v.Name, Name: v.Name, Labels: v.Labels, Size: size})
	}
	return candidates, nil
}

func (d *DockerHost) imageCandidates(ctx context.Context, kind dockerhost.Kind) ([]dockerhost.Candidate, error) {
	var images []struct {
		ID          string `json:"Id"`
		RepoTags    []string
		RepoDigests []string
		Labels      map[string]string
		Size        int64
	}
	if err := d.source.get(ctx, "/images/json", &images); err != nil {
		return nil, err
	}
	var containers []struct{ ImageID string }
	if err := d.source.get(ctx, "/containers/json?all=1", &containers); err != nil {
		return nil, err
	}
	used := make(map[string]bool, len(containers))
	for _, c := range containers {
		used[c.ImageID] = true
	}
	candidates := []dockerhost.Candidate{}
	for _, image := range images {
		if used[image.ID] {
			continue
		}
		tags := []string{}
		for _, tag := range image.RepoTags {
			if tag != "<none>:<none>" {
				tags = append(tags, tag)
			}
		}
		if kind == dockerhost.Images && len(tags) > 0 {
			continue
		}
		name := image.ID
		if len(tags) > 0 {
			name = tags[0]
		}
		candidates = append(candidates, dockerhost.Candidate{ID: image.ID, Name: name, Labels: image.Labels, Tags: tags, Digests: image.RepoDigests, Size: bytesAvailable(image.Size)})
	}
	return candidates, nil
}

// Remove deletes one volume or image without force. Docker's 409 (in use,
// conflict) is a refusal; any other failure is an error.
func (d *DockerHost) Remove(ctx context.Context, kind dockerhost.Kind, c dockerhost.Candidate) (bool, string, error) {
	switch kind {
	case dockerhost.Volumes, dockerhost.AllVolumes:
		return d.delete(ctx, "/volumes/"+c.Name)
	case dockerhost.Images, dockerhost.UnusedImages:
		// An image with several tags cannot be deleted by id without force;
		// untag each, and Docker deletes the image with the last one.
		references := []string{c.ID}
		if len(c.Tags) > 1 {
			references = c.Tags
		}
		for _, reference := range references {
			if removed, refusal, err := d.delete(ctx, "/images/"+reference); !removed {
				return false, refusal, err
			}
		}
		return true, "", nil
	}
	return false, "", fmt.Errorf("%s cannot be removed one by one", kind)
}

func (d *DockerHost) delete(ctx context.Context, path string) (bool, string, error) {
	target := url.URL{Scheme: "http", Host: "docker", Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target.String(), nil)
	if err != nil {
		return false, "", err
	}
	resp, err := d.source.client.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var body struct{ Message string }
	message := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &body) == nil && body.Message != "" {
		message = body.Message
	}
	switch {
	case resp.StatusCode/100 == 2:
		return true, "", nil
	case resp.StatusCode == http.StatusConflict:
		if message == "" {
			message = "Docker refused: conflict"
		}
		return false, message, nil
	case resp.StatusCode == http.StatusNotFound:
		return false, "already gone", nil
	}
	return false, "", fmt.Errorf("Docker delete %s: %s: %s", path, resp.Status, message)
}
