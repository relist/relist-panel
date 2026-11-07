package registryauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Auth is one registry login written into a Docker config.
type Auth struct {
	Host, Username, Secret string
}

// Source lists the logins every pull or build gets.
type Source interface {
	Auths(ctx context.Context) ([]Auth, error)
}

// Auths makes the Store a Source: one login per External registry.
func (s *Store) Auths(ctx context.Context) ([]Auth, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Auth, 0, len(list))
	for _, e := range list {
		out = append(out, Auth{Host: e.Host, Username: e.Username, Secret: e.Secret})
	}
	return out, nil
}

type multi []Source

// MultiSource concatenates the logins of each non-nil source, in order.
func MultiSource(sources ...Source) Source { return multi(sources) }

func (m multi) Auths(ctx context.Context) ([]Auth, error) {
	var out []Auth
	for _, s := range m {
		if s == nil {
			continue
		}
		auths, err := s.Auths(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, auths...)
	}
	return out, nil
}

// Switch is a Source set after it is handed out: main builds the Docker
// runner before the handler builds the stores that feed it. Unset, it lists
// no logins.
type Switch struct {
	mu  sync.RWMutex
	src Source
}

func (s *Switch) Set(src Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.src = src
}

func (s *Switch) Auths(ctx context.Context) ([]Auth, error) {
	s.mu.RLock()
	src := s.src
	s.mu.RUnlock()
	if src == nil {
		return nil, nil
	}
	return src.Auths(ctx)
}

// dockerHubKey is the auths key the Docker CLI reads Docker Hub's login from.
const dockerHubKey = "https://index.docker.io/v1/"

// WriteConfig writes dir/config.json, mode 0600, with one auths entry per
// login.
func WriteConfig(dir string, auths []Auth) error {
	type entry struct {
		Auth string `json:"auth"`
	}
	cfg := struct {
		Auths map[string]entry `json:"auths"`
	}{Auths: map[string]entry{}}
	for _, a := range auths {
		key := a.Host
		if key == DockerHub {
			key = dockerHubKey
		}
		cfg.Auths[key] = entry{Auth: base64.StdEncoding.EncodeToString([]byte(a.Username + ":" + a.Secret))}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600)
}
