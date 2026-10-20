package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"

	"relist-panel/internal/volfs"
)

// ErrUnknownItem is an item ID the manifest does not list.
var ErrUnknownItem = errors.New("no such migration item")

// StreamItem writes one item and, when the whole stream was written,
// records its SHA-256 for Digest. Items stream only once the manifest exists.
func (s *Source) StreamItem(ctx context.Context, id string, w io.Writer) error {
	step, st, err := s.d.Store.Lock(ctx)
	if err != nil {
		return err
	}
	if (step != StepReady && step != StepMigrated) || st.Manifest == nil {
		return ErrNotReady
	}
	var item *Item
	for i := range st.Manifest.Items {
		if st.Manifest.Items[i].ID == id {
			item = &st.Manifest.Items[i]
		}
	}
	if item == nil {
		return ErrUnknownItem
	}
	sending := &sendingItem{id: id}
	s.mu.Lock()
	delete(s.digests, id)
	s.sending = sending
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.sending == sending {
			s.sending = nil
		}
		s.mu.Unlock()
	}()

	h := sha256.New()
	out := io.MultiWriter(sending, w, h)
	if err := s.writeItem(ctx, *item, out); err != nil {
		return fmt.Errorf("stream %s: %w", id, err)
	}
	s.mu.Lock()
	s.digests[id] = hex.EncodeToString(h.Sum(nil))
	s.sent[id] = true
	s.mu.Unlock()
	return nil
}

// Activity is what the old host is sending, for `relist-panel migration
// serve` (ADR-0019): the item streamed last, its bytes so far, and how many
// items were sent whole, each counted once however often it was retried.
type Activity struct {
	Item  string `json:"item"`
	Bytes int64  `json:"bytes"`
	Done  int    `json:"done"`
}

// sendingItem counts one stream's bytes as they are written.
type sendingItem struct {
	id    string
	bytes atomic.Int64
}

func (c *sendingItem) Write(p []byte) (int, error) {
	c.bytes.Add(int64(len(p)))
	return len(p), nil
}

func (s *Source) Activity() Activity {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := Activity{Done: len(s.sent)}
	if s.sending != nil {
		a.Item = s.sending.id
		a.Bytes = s.sending.bytes.Load()
	}
	return a
}

// Digest is the SHA-256 of the last complete stream of id.
func (s *Source) Digest(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.digests[id]
	return d, ok
}

func (s *Source) writeItem(ctx context.Context, item Item, w io.Writer) error {
	switch item.Kind {
	case KindDatabase:
		return s.writeDatabase(ctx, w)
	case KindDataDir:
		root, err := volfs.Open(s.d.DataDir)
		if err != nil {
			return err
		}
		defer root.Close()
		return root.PackExcluding(".", skipInDataDir, w)
	case KindVolume:
		return s.d.Docker.Pack(ctx, item.Volume.Name, ".", w)
	case KindHostPath:
		return s.d.Docker.Pack(ctx, filepath.Dir(item.HostPath), filepath.Base(item.HostPath), w)
	case KindImage:
		refs := item.Image.Tags
		if len(refs) == 0 {
			refs = []string{item.Image.ID}
		}
		return s.d.Docker.SaveImages(ctx, refs, w)
	}
	return fmt.Errorf("unknown item kind %q", item.Kind)
}

// writeDatabase streams a consistent copy of panel.db made with VACUUM INTO,
// into a directory of its own: a retried stream may overlap one still
// running, and VACUUM INTO refuses a file that exists.
func (s *Source) writeDatabase(ctx context.Context, w io.Writer) error {
	base := filepath.Join(s.d.DataDir, ".migration-out")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(base, "db-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "panel.db")
	if _, err := s.d.DB.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}
