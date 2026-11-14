package backupdest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type s3Remote struct {
	c      *minio.Client
	cfg    S3Config
	secret string
}

// NewS3 connects to an S3-compatible bucket. Nothing is sent until a call.
func NewS3(d Destination) (Remote, error) {
	u, err := url.Parse(d.S3.Endpoint)
	if err != nil || u.Host == "" {
		return nil, errors.New("the endpoint is not a URL")
	}
	lookup := minio.BucketLookupAuto
	if d.S3.PathStyle {
		lookup = minio.BucketLookupPath
	}
	c, err := minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(d.S3.AccessKeyID, d.Secret, ""),
		Secure:       u.Scheme == "https",
		Region:       d.S3.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, errors.New(scrub(err.Error(), d.Secret))
	}
	return &s3Remote{c: c, cfg: d.S3, secret: d.Secret}, nil
}

// scrub removes the secret from an error text before it leaves this package.
func scrub(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "***")
}

func (r *s3Remote) fail(err error) error { return errors.New(scrub(err.Error(), r.secret)) }

func (r *s3Remote) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	if size < 0 {
		return errors.New("upload needs the file size")
	}
	if _, err := r.c.PutObject(ctx, r.cfg.Bucket, key, body, size, minio.PutObjectOptions{ContentType: "application/gzip"}); err != nil {
		return r.fail(err)
	}
	st, err := r.c.StatObject(ctx, r.cfg.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return r.fail(err)
	}
	if st.Size != size {
		return fmt.Errorf("the object is %d bytes in the bucket, expected %d", st.Size, size)
	}
	return nil
}

func (r *s3Remote) Delete(ctx context.Context, key string) error {
	err := r.c.RemoveObject(ctx, r.cfg.Bucket, key, minio.RemoveObjectOptions{})
	if err != nil && minio.ToErrorResponse(err).Code != "NoSuchKey" {
		return r.fail(err)
	}
	return nil
}

func (r *s3Remote) Test(ctx context.Context) error {
	var tag [8]byte
	if _, err := rand.Read(tag[:]); err != nil {
		return err
	}
	key := ".relist-test-" + hex.EncodeToString(tag[:])
	if r.cfg.Prefix != "" {
		key = r.cfg.Prefix + "/" + key
	}
	want := []byte("relist backup destination test " + hex.EncodeToString(tag[:]))
	if _, err := r.c.PutObject(ctx, r.cfg.Bucket, key, bytes.NewReader(want), int64(len(want)), minio.PutObjectOptions{}); err != nil {
		return r.fail(err)
	}
	// Whatever happens next, do not leave the test object behind.
	defer func() {
		_ = r.c.RemoveObject(context.WithoutCancel(ctx), r.cfg.Bucket, key, minio.RemoveObjectOptions{})
	}()
	obj, err := r.c.GetObject(ctx, r.cfg.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return r.fail(err)
	}
	defer obj.Close()
	got, err := io.ReadAll(obj)
	if err != nil {
		return r.fail(err)
	}
	if !bytes.Equal(got, want) {
		return errors.New("the object read back is not what was written")
	}
	return r.Delete(ctx, key)
}
