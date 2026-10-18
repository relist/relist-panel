package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"relist-panel/internal/volfs"
)

// restorer writes each item kind on the new host. The database and the data
// directory go to staging, moved into place at finalize; Volumes, host paths
// and Images go straight to Docker, because nothing serves them yet.
type restorer struct {
	local   LocalHost
	staging string
}

// NewRestorer returns the Restorer, which is also an ImageRestorer.
func NewRestorer(local LocalHost, stagingDir string) Restorer {
	return &restorer{local: local, staging: stagingDir}
}

var _ ImageRestorer = (*restorer)(nil)

func (r *restorer) stagedData() string { return filepath.Join(r.staging, "data") }
func (r *restorer) stagedDB() string   { return filepath.Join(r.staging, "panel.db") }

func (r *restorer) Restore(ctx context.Context, item Item, body io.Reader) error {
	switch item.Kind {
	case KindDatabase:
		if err := os.MkdirAll(r.staging, 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(r.stagedDB(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, body); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	case KindDataDir:
		if err := os.MkdirAll(r.stagedData(), 0o750); err != nil {
			return err
		}
		root, err := volfs.Open(r.stagedData())
		if err != nil {
			return err
		}
		defer root.Close()
		return root.Unpack(body)
	case KindVolume:
		if err := r.local.Docker.CreateVolume(ctx, *item.Volume); err != nil {
			return fmt.Errorf("create volume %s: %w", item.Volume.Name, err)
		}
		return r.local.Docker.Unpack(ctx, item.Volume.Name, body)
	case KindHostPath:
		// Docker creates a missing bind source directory; the stream's one
		// top entry is the path's base name.
		return r.local.Docker.Unpack(ctx, filepath.Dir(item.HostPath), body)
	case KindImage:
		return errors.New("images are restored with RestoreImage")
	}
	return fmt.Errorf("unknown item kind %q", item.Kind)
}

// Cleanup removes what a failed attempt wrote. Preflight made sure nothing
// was there before, so removing the target removes only Migration data.
func (r *restorer) Cleanup(ctx context.Context, item Item) error {
	switch item.Kind {
	case KindDatabase:
		if err := os.Remove(r.stagedDB()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	case KindDataDir:
		return os.RemoveAll(r.stagedData())
	case KindVolume:
		return r.local.Docker.RemoveVolume(ctx, item.Volume.Name)
	case KindHostPath:
		// The path may not exist yet; the helper's refusal then means
		// there is nothing to clean.
		r.local.Docker.Delete(ctx, filepath.Dir(item.HostPath), filepath.Base(item.HostPath))
	}
	return nil
}

// Discard removes everything an aborted Migration restored.
func (r *restorer) Discard(ctx context.Context, items []Item) error {
	var errs []error
	for _, item := range items {
		switch item.Kind {
		case KindImage:
			for _, tag := range imageTags(*item.Image) {
				errs = append(errs, r.local.Docker.RemoveImage(ctx, tag))
			}
		default:
			errs = append(errs, r.Cleanup(ctx, item))
		}
	}
	if err := os.RemoveAll(r.staging); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// imageTags is the tags an Image has on the new host after restore.
func imageTags(img ImageInfo) []string {
	if len(img.Tags) == 0 {
		return []string{migratedTag(img)}
	}
	return img.Tags
}

func migratedTag(img ImageInfo) string { return "relist-migrated/" + shortID(img.ID) }

// RestoreImage pulls an Image by digest and re-applies its tags; one without
// digests, or whose pull fails or brings another Image, is loaded from the
// old host's `docker save` instead.
func (r *restorer) RestoreImage(ctx context.Context, item Item, fetch func(context.Context) (io.ReadCloser, error)) (string, error) {
	img := *item.Image
	docker := r.local.Docker
	for _, digest := range img.RepoDigests {
		if err := docker.PullImage(ctx, digest); err != nil {
			continue
		}
		if pulled, ok, err := docker.InspectImage(ctx, digest); err != nil || !ok || pulled.ID != img.ID {
			break
		}
		for _, tag := range img.Tags {
			if err := docker.TagImage(ctx, digest, tag); err != nil {
				return "", fmt.Errorf("tag %s: %w", tag, err)
			}
		}
		return "", nil
	}

	body, err := fetch(ctx)
	if err != nil {
		return "", err
	}
	loaded, err := docker.LoadImages(ctx, body)
	if _, drainErr := io.Copy(io.Discard, body); err == nil {
		err = drainErr
	}
	body.Close()
	if err != nil {
		return "", fmt.Errorf("load image %s: %w", shortID(img.ID), err)
	}
	ref := img.ID
	if len(img.Tags) == 0 {
		// Tag what docker load reports: another image store gives the
		// same content another ID.
		source := img.ID
		for _, l := range loaded {
			if strings.HasPrefix(l, "sha256:") {
				source = l
			}
		}
		if err := docker.TagImage(ctx, source, migratedTag(img)); err != nil {
			return "", fmt.Errorf("tag image %s: %w", shortID(img.ID), err)
		}
		ref = migratedTag(img)
	} else {
		ref = img.Tags[0]
	}
	if loaded, ok, err := docker.InspectImage(ctx, ref); err != nil {
		return "", err
	} else if !ok || loaded.ID != img.ID {
		return fmt.Sprintf("Image %s has a different ID on this host (image store differs); content was loaded from the source", ref), nil
	}
	return "", nil
}
