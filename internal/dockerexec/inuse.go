package dockerexec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"relist-panel/internal/vulnscan"
)

const (
	composeServiceLabel = "com.docker.compose.service"
)

// ComposeIdentities maps each Compose's deploy project label (ADR-0005) to
// its IDs.
type ComposeIdentities func(ctx context.Context) (map[string]vulnscan.ImageUser, error)

// InUse implements vulnscan.InUse over GET /containers/json. A container is
// a Service when its project label is a known Compose; the panel and Traefik
// are the containers of the panel's own install project, found through the
// panel container's hostname. Everything else is ignored.
type InUse struct {
	ContainerSource
	identities ComposeIdentities
	hostname   func() (string, error)
}

var _ vulnscan.InUse = (*InUse)(nil)

func NewInUse(socketPath string, identities ComposeIdentities, hostname func() (string, error)) *InUse {
	return &InUse{ContainerSource: ContainerSource{client: newSocketClient(socketPath)}, identities: identities, hostname: hostname}
}

func (u *InUse) Images(ctx context.Context) ([]vulnscan.UsedImage, error) {
	known, err := u.identities(ctx)
	if err != nil {
		return nil, err
	}
	var containers []struct {
		ImageID string            `json:"ImageID"`
		Labels  map[string]string `json:"Labels"`
	}
	if err := u.get(ctx, "/containers/json", &containers); err != nil {
		return nil, err
	}
	install := u.installProject(ctx)

	var out []vulnscan.UsedImage
	index := map[string]int{}
	for _, c := range containers {
		project, service := c.Labels[composeProjectLabel], c.Labels[composeServiceLabel]
		var user vulnscan.ImageUser
		if compose, ok := known[project]; ok && project != "" {
			user = compose
			user.Kind, user.Service = vulnscan.UserService, service
		} else if install != "" && project == install && service == "panel" {
			user = vulnscan.ImageUser{Kind: vulnscan.UserPanel}
		} else if install != "" && project == install && service == "traefik" {
			user = vulnscan.ImageUser{Kind: vulnscan.UserTraefik}
		} else {
			continue
		}
		i, ok := index[c.ImageID]
		if !ok {
			refs, err := u.refs(ctx, c.ImageID)
			var apiErr *engineError
			if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
				continue // the image went between the list and this inspect: a redeploy race
			}
			if err != nil {
				return nil, err
			}
			i = len(out)
			index[c.ImageID] = i
			out = append(out, vulnscan.UsedImage{ImageID: c.ImageID, Refs: refs})
		}
		out[i].Users = append(out[i].Users, user)
	}
	return out, nil
}

// installProject is the panel's own compose project, "" when the panel is
// not running in a container (a bare binary during development).
func (u *InUse) installProject(ctx context.Context) string {
	name, err := u.hostname()
	if err != nil || name == "" {
		return ""
	}
	var self struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := u.get(ctx, "/containers/"+url.PathEscape(name)+"/json", &self); err != nil {
		return ""
	}
	return self.Config.Labels[composeProjectLabel]
}

func (u *InUse) refs(ctx context.Context, imageID string) ([]string, error) {
	var image struct {
		RepoTags []string `json:"RepoTags"`
	}
	if err := u.get(ctx, "/images/"+url.PathEscape(imageID)+"/json", &image); err != nil {
		return nil, fmt.Errorf("inspect %s: %w", imageID, err)
	}
	refs := []string{}
	for _, tag := range image.RepoTags {
		if tag != "<none>:<none>" {
			refs = append(refs, tag)
		}
	}
	return refs, nil
}
