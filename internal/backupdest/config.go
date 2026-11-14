// Package backupdest owns Backup destinations: the S3-compatible buckets a
// Database's scheduled Backups are copied to (ADR-0040). A destination's
// secret lives in the panel database and never leaves it: no response, log,
// error or audit detail carries it.
package backupdest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// TypeS3 is the only destination type phase 3 ships.
const TypeS3 = "s3"

// S3Config is where an S3-compatible bucket is and how to reach it. The
// secret access key is not part of it: Destination keeps it apart.
type S3Config struct {
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	AccessKeyID string `json:"accessKeyId"`
	PathStyle   bool   `json:"pathStyle"`
}

// Destination is one stored Backup destination.
type Destination struct {
	ID            int64
	Name, Type    string
	S3            S3Config
	Secret        string
	LastTestAt    *time.Time
	LastTestOK    bool
	LastTestError string
}

// Remote is a connection to a destination's bucket.
type Remote interface {
	// Put uploads size bytes of r under key and checks the object's size after.
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	// Delete removes key; a missing object is not an error.
	Delete(ctx context.Context, key string) error
	// Test writes a small object under the prefix, reads it back and deletes it.
	Test(ctx context.Context) error
}

// Opener connects to a stored destination.
type Opener interface {
	Open(ctx context.Context, id int64) (Destination, Remote, error)
}

// Usage is what still uses a destination: the Databases whose schedule copies
// to it, and the Backup copies it holds.
type Usage struct {
	Schedules []string
	Copies    int
}

// ErrInUse is a destination a schedule still copies to.
type ErrInUse struct{ Schedules []string }

func (e ErrInUse) Error() string {
	return "used by the schedules of: " + strings.Join(e.Schedules, ", ")
}

// ErrCopies is a delete that did not confirm the number of copies it leaves
// in the bucket.
type ErrCopies struct{ Copies int }

func (e ErrCopies) Error() string {
	return fmt.Sprintf("%d backup copies stay in the bucket and the panel stops tracking them", e.Copies)
}

// ErrNotFound is a destination that does not exist.
var ErrNotFound = errors.New("Backup destination not found")

// ErrDuplicateName is a second destination with one name.
var ErrDuplicateName = errors.New("a Backup destination with this name already exists")

// ValidateS3 trims and checks c. The endpoint is http(s)://host[:port] with
// no path, the bucket is one name, and the prefix loses its slashes at both ends.
func ValidateS3(c S3Config) (S3Config, error) {
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.Region = strings.TrimSpace(c.Region)
	c.Bucket = strings.TrimSpace(c.Bucket)
	c.Prefix = strings.Trim(strings.TrimSpace(c.Prefix), "/")
	c.AccessKeyID = strings.TrimSpace(c.AccessKeyID)
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || u.User != nil ||
		strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return S3Config{}, &FieldError{"endpoint", "endpoint must be http:// or https:// and a host, such as https://s3.amazonaws.com or http://minio:9000"}
	}
	c.Endpoint = u.Scheme + "://" + u.Host
	if c.Bucket == "" || strings.Contains(c.Bucket, "/") {
		return S3Config{}, &FieldError{"bucket", "bucket is required and takes no slash"}
	}
	if c.AccessKeyID == "" {
		return S3Config{}, &FieldError{"accessKeyId", "access key ID is required"}
	}
	return c, nil
}

// FieldError names the form field at fault.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Msg }

// ObjectKey is where a Backup's file goes in the bucket:
// <prefix>/<db slug>-<db id>/<logical database>/<file name>. The logical
// database keeps [A-Za-z0-9._-]; any other byte becomes "_".
func ObjectKey(prefix, dbSlug string, dbID int64, logical, file string) string {
	clean := []byte(logical)
	for i, b := range clean {
		ok := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '_' || b == '-'
		if !ok {
			clean[i] = '_'
		}
	}
	name := string(clean)
	if name == "." || name == ".." {
		name = strings.Repeat("_", len(name))
	}
	var parts []string
	for _, p := range []string{prefix, fmt.Sprintf("%s-%d", dbSlug, dbID), name, file} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}
