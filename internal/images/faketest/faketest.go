// Package faketest provides the test double for the images package's Images
// seam: HTTP-seam tests install it in place of the real Docker Engine API
// adapter, script its answers, and assert the archive bytes it received — all
// with no Docker in them. It lives in its own package so nothing but tests
// imports it.
package faketest

import (
	"context"
	"io"
	"sync"

	"relist-panel/internal/images"
)

// Images is a scripted images.Images. Its zero value lists nothing and loads
// successfully with no names.
type Images struct {
	Listed  []images.Image
	ListErr error
	Names   []string
	LoadErr error
	// ReadLimit, when positive, makes Load read only that many bytes of the
	// archive and then return LoadErr, as a daemon rejecting early does.
	ReadLimit int

	mu     sync.Mutex
	Loaded [][]byte
}

// List returns the scripted listing.
func (f *Images) List(context.Context) ([]images.Image, error) {
	return f.Listed, f.ListErr
}

// Load reads the whole archive, records it, and returns the scripted answer.
// A failed read is returned instead.
func (f *Images) Load(_ context.Context, archive io.Reader, _ int64) ([]string, error) {
	if f.ReadLimit > 0 {
		_, _ = io.CopyN(io.Discard, archive, int64(f.ReadLimit))
		return nil, f.LoadErr
	}
	data, err := io.ReadAll(archive)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.Loaded = append(f.Loaded, data)
	f.mu.Unlock()
	return f.Names, f.LoadErr
}

// LoadedArchives returns a copy of the archives Load has received.
func (f *Images) LoadedArchives() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.Loaded...)
}
