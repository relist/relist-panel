// Package backupdest owns Backup destinations: the S3-compatible buckets and SFTP servers a
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
	"path"
	"strings"
	"time"
	"unicode"
)

// The destination types.
const (
	TypeS3   = "s3"
	TypeSFTP = "sftp"
)

// How an SFTP destination logs in.
const (
	AuthKey      = "key"
	AuthPassword = "password"
)

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

// SFTPConfig is where an SFTP server is and how to reach it. The password is
// not part of it: Destination keeps it apart as Secret.
type SFTPConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Path string `json:"path"`
	Auth string `json:"auth"`
}

// Destination is one stored Backup destination.
type Destination struct {
	ID         int64
	Name, Type string
	S3         S3Config
	SFTP       SFTPConfig
	// SSHKeyID is the library key of an SFTP destination with key auth.
	SSHKeyID *int64
	// SSHKeyName, PrivateKey and PublicKey are joined from ssh_keys; the
	// store never saves them.
	SSHKeyName, PrivateKey, PublicKey string
	// HostKey is the trusted server host key, one authorized_keys line, or "".
	HostKey string
	Secret  string
	// Encrypt turns copies to this destination into age files; Passphrase is
	// their scrypt passphrase, a secret like Secret.
	Encrypt       bool
	Passphrase    string
	LastTestAt    *time.Time
	LastTestOK    bool
	LastTestError string
}

// KeyPrefix is the part of an object key that comes before the Database's
// directory: the S3 prefix, or the SFTP base path.
func (d Destination) KeyPrefix() string {
	if d.Type == TypeSFTP {
		return d.SFTP.Path
	}
	return d.S3.Prefix
}

// ErrHostKeyChanged is a server that presents another host key than the one
// recorded on its first successful Test.
var ErrHostKeyChanged = errors.New("the server's host key changed; if you expected this, use Reset host key and Test again")

// ErrNoHostKey is an SFTP destination that no Test has trusted yet.
var ErrNoHostKey = errors.New("this SFTP destination has no trusted host key yet: run Test first")

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
	return fmt.Sprintf("%d backup copies stay in the destination and the panel stops tracking them", e.Copies)
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

// ValidateSFTP trims and checks c. A zero port becomes 22. The base path is
// cleaned: "" and "." mean the login directory, a leading "/" is kept, "/"
// and ".." are refused.
func ValidateSFTP(c SFTPConfig) (SFTPConfig, error) {
	c.Host = strings.TrimSpace(c.Host)
	c.User = strings.TrimSpace(c.User)
	c.Path = strings.TrimSpace(c.Path)
	if c.Host == "" || strings.ContainsFunc(c.Host, func(r rune) bool { return r == '/' || unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return SFTPConfig{}, &FieldError{"host", "host is required and takes no slash or space"}
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.Port < 1 || c.Port > 65535 {
		return SFTPConfig{}, &FieldError{"port", "port must be between 1 and 65535"}
	}
	if c.User == "" || strings.ContainsFunc(c.User, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return SFTPConfig{}, &FieldError{"user", "user is required and takes no space"}
	}
	if c.Auth != AuthKey && c.Auth != AuthPassword {
		return SFTPConfig{}, &FieldError{"auth", `auth must be "key" or "password"`}
	}
	if c.Path != "" {
		for _, el := range strings.Split(c.Path, "/") {
			if el == ".." {
				return SFTPConfig{}, &FieldError{"path", "base path cannot contain .."}
			}
		}
		c.Path = path.Clean(c.Path)
		if c.Path == "." {
			c.Path = ""
		}
		if c.Path == "/" {
			return SFTPConfig{}, &FieldError{"path", "base path cannot be /"}
		}
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
