package databases

import (
	"time"

	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/agefile"
	"relist-panel/internal/backupdest"
)

// fakeRemote is an in-memory bucket.
type fakeRemote struct {
	mu      sync.Mutex
	objects map[string][]byte
	sizes   map[string]int64 // the size Put was told
	putErr  error
	delErr  error
	// hang makes Put and Delete read nothing and wait for their context, as
	// a bucket that stopped answering does.
	hang    bool
	deletes []string
}

func newFakeRemote() *fakeRemote {
	return &fakeRemote{objects: map[string][]byte{}, sizes: map[string]int64{}}
}

func (f *fakeRemote) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if f.hung() {
		<-ctx.Done()
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.objects[key], f.sizes[key] = b, size
	return nil
}

func (f *fakeRemote) hung() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hang
}

func (f *fakeRemote) Delete(ctx context.Context, key string) error {
	if f.hung() {
		<-ctx.Done()
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delErr != nil {
		return f.delErr
	}
	f.deletes = append(f.deletes, key)
	delete(f.objects, key)
	return nil
}

func (f *fakeRemote) Test(context.Context) error { return nil }

func (f *fakeRemote) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok
}

func (f *fakeRemote) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.objects)
}

type fakeOpener struct {
	remote *fakeRemote
	// dest replaces the default S3 destination "minio" when set.
	dest *backupdest.Destination
}

func (o fakeOpener) Open(_ context.Context, id int64) (backupdest.Destination, backupdest.Remote, error) {
	if id != 1 {
		return backupdest.Destination{}, nil, backupdest.ErrNotFound
	}
	if o.dest != nil {
		return *o.dest, o.remote, nil
	}
	return backupdest.Destination{ID: 1, Name: "minio", Type: "s3", S3: backupdest.S3Config{Prefix: "pfx"}}, o.remote, nil
}

// withDestination inserts destination 1 ("minio") and wires the fake bucket.
func (r *rig) withDestination(t *testing.T) *fakeRemote {
	t.Helper()
	if _, err := r.store.db.Exec(`INSERT INTO backup_destinations (id, name, type, config, secret, created_at, updated_at) VALUES (1, 'minio', 's3', '{}', 'x', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	remote := newFakeRemote()
	r.svc.d.Destinations = fakeOpener{remote: remote}
	return remote
}

func (r *rig) toDestination(sc *Schedule) {
	id := int64(1)
	sc.DestinationID = &id
}

func TestScheduledRunUploads(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.host.script("psql -U postgres -At -c SELECT datname", func(req ExecRequest) (int, error) {
		_, err := req.Stdout.Write([]byte("app\nweb\n"))
		return 0, err
	})
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		if strings.Contains(strings.Join(req.Env, " "), "PGDATABASE=web") {
			return 1, nil // web's dump fails: it gets no upload
		}
		_, err := req.Stdout.Write([]byte("SELECT 1;\n"))
		return 0, err
	})
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	var app, web Backup
	for _, b := range r.backups(t, d.ID) {
		switch b.LogicalName {
		case "app":
			app = b
		case "web":
			web = b
		}
	}
	if app.Upload == nil || app.Upload.Status != UploadUploaded || app.Upload.DestinationName != "minio" {
		t.Fatalf("app upload = %+v", app.Upload)
	}
	key := backupdest.ObjectKey("pfx", "shop", d.ID, "app", app.FileName)
	if app.Upload.ObjectKey != key || !remote.has(key) {
		t.Fatalf("key = %q, bucket has %v", app.Upload.ObjectKey, remote.objects)
	}
	fi, err := os.Stat(r.svc.filePath(d.ID, app))
	if err != nil {
		t.Fatal(err)
	}
	if remote.sizes[key] != fi.Size() || fi.Size() <= 0 {
		t.Fatalf("Put was told size %d, the file is %d", remote.sizes[key], fi.Size())
	}
	if web.Status != JobFailed || web.Upload != nil {
		t.Fatalf("failed dump = %+v", web)
	}
	if remote.count() != 1 {
		t.Fatalf("bucket has %d objects", remote.count())
	}
}

// The object key's prefix comes from the destination's type: an SFTP
// destination's base path, kept in the recorded key.
func TestUploadKeyUsesSFTPPath(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.svc.d.Destinations = fakeOpener{remote: remote, dest: &backupdest.Destination{
		ID: 1, Name: "box", Type: backupdest.TypeSFTP, HostKey: "ssh-ed25519 AAAA",
		SFTP: backupdest.SFTPConfig{Host: "box", Port: 22, User: "u", Path: "/srv/b", Auth: backupdest.AuthPassword}}}
	r.host.script("psql -U postgres -At -c SELECT datname", func(req ExecRequest) (int, error) {
		_, err := req.Stdout.Write([]byte("app\n"))
		return 0, err
	})
	r.host.script("pg_dump -U postgres", func(req ExecRequest) (int, error) {
		_, err := req.Stdout.Write([]byte("SELECT 1;\n"))
		return 0, err
	})
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	bs := r.backups(t, d.ID)
	if len(bs) != 1 || bs[0].Upload == nil || bs[0].Upload.Status != UploadUploaded {
		t.Fatalf("backups = %+v", bs)
	}
	if want := "/srv/b/" + "shop-" + strconv.FormatInt(d.ID, 10) + "/"; !strings.HasPrefix(bs[0].Upload.ObjectKey, want) {
		t.Fatalf("object key = %q, want prefix %q", bs[0].Upload.ObjectKey, want)
	}
}

func TestUploadFailureKeepsBackup(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	remote.putErr = errors.New("connection refused")
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	b := r.lastBackup(t, d.ID)
	if b.Status != JobSucceeded || b.Upload == nil || b.Upload.Status != UploadFailed || b.Upload.Error != "connection refused" {
		t.Fatalf("backup = %+v upload = %+v", b, b.Upload)
	}
	if sc := r.getSchedule(t, d.ID); sc.LastError != "upload to minio failed: connection refused" {
		t.Fatalf("last_error = %q", sc.LastError)
	}
	if _, err := os.Stat(r.svc.filePath(d.ID, b)); err != nil {
		t.Fatalf("the local file is gone: %v", err)
	}
}

func TestRetryUpload(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	remote.putErr = errors.New("connection refused")
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	b := r.lastBackup(t, d.ID)

	remote.mu.Lock()
	remote.putErr = nil
	remote.mu.Unlock()
	if err := r.svc.RetryUpload(context.Background(), d.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	r.svc.Wait()
	b = r.lastBackup(t, d.ID)
	if b.Upload == nil || b.Upload.Status != UploadUploaded || !remote.has(b.Upload.ObjectKey) {
		t.Fatalf("after retry = %+v", b.Upload)
	}
	if err := r.svc.RetryUpload(context.Background(), d.ID, b.ID); !errors.Is(err, ErrNoFailedUpload) {
		t.Fatalf("retry of an uploaded copy = %v", err)
	}
	// A Remote only Backup has no file to send.
	if err := r.store.SetLocalRemoved(context.Background(), b.ID, r.svc.d.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.RetryUpload(context.Background(), d.ID, b.ID); !errors.Is(err, ErrRemoteOnly) {
		t.Fatalf("retry of a Remote only Backup = %v", err)
	}
	if r.svc.Busy() {
		t.Fatal("job slot not freed")
	}
}

func TestStartupFailsInterruptedUploads(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	r.withDestination(t)
	b := r.manualBackup(t, d.ID)
	u, err := r.store.InsertUpload(context.Background(), Upload{BackupID: b.ID, DestinationID: 1, ObjectKey: "k", Status: UploadRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := r.lastBackup(t, d.ID)
	if got.Upload == nil || got.Upload.ID != u.ID || got.Upload.Status != UploadFailed || got.Upload.Error != "the panel stopped during this run" {
		t.Fatalf("upload after Startup = %+v", got.Upload)
	}
}

func TestScheduleDestinationMustExist(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	sc := DefaultSchedule(d.ID)
	one := int64(1)
	sc.DestinationID = &one
	if _, err := r.svc.SaveSchedule(context.Background(), sc); !errors.As(err, new(ErrInvalidSchedule)) {
		t.Fatalf("no destinations wired = %v", err)
	}
	r.withDestination(t)
	if saved, err := r.svc.SaveSchedule(context.Background(), sc); err != nil || saved.DestinationID == nil || *saved.DestinationID != 1 {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	two := int64(2)
	sc.DestinationID = &two
	if _, err := r.svc.SaveSchedule(context.Background(), sc); !errors.As(err, new(ErrInvalidSchedule)) {
		t.Fatalf("unknown destination = %v", err)
	}
}

func TestUploadRoutes(t *testing.T) {
	a := newAPIRig(t)
	a.seed(t, "Shop", "postgres", "18")
	remote := a.withDestination(t)
	remote.putErr = errors.New("connection refused")
	a.dumps("pg_dump -U postgres", "SELECT 1;\n")
	a.useClock(day(15, 3, 0))
	a.schedule(t, 1, a.toDestination, day(14, 3, 0))
	a.svc.tick(context.Background())
	list := a.do("GET", "/api/databases/1/backups", "").Body.String()
	if !strings.Contains(list, `"upload":{"destination":"minio","status":"failed","error":"connection refused"`) {
		t.Fatalf("list = %s", list)
	}
	if w := a.do("GET", "/api/databases/1/schedule", ""); !strings.Contains(w.Body.String(), `"destinationId":1`) {
		t.Fatalf("schedule = %s", w.Body)
	}
	remote.mu.Lock()
	remote.putErr = nil
	remote.mu.Unlock()
	if w := a.do("POST", "/api/databases/1/backups/1/retry-upload", ""); w.Code != 202 {
		t.Fatalf("retry = %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/databases/1/backups/1/retry-upload", ""); w.Code != 409 {
		t.Fatalf("retry again = %d %s", w.Code, w.Body)
	}
}

// A bucket that stops answering mid-upload must not hold the Database's job
// slot, and with it the host-wide scheduler, for ever.
func TestUploadStallIsCutOff(t *testing.T) {
	r := newRig(t)
	r.svc.uploadStall = 50 * time.Millisecond
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	remote.hang = true
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	done := make(chan struct{})
	go func() { r.svc.tick(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the tick is still waiting on a bucket that does not answer")
	}
	b := r.lastBackup(t, d.ID)
	if b.Status != JobSucceeded || b.Upload == nil || b.Upload.Status != UploadFailed || !strings.Contains(b.Upload.Error, "no progress") {
		t.Fatalf("backup = %+v upload = %+v", b, b.Upload)
	}
	if job := r.svc.Jobs(d.ID); job != "" {
		t.Fatalf("job slot still held: %q", job)
	}
}

// A delete the bucket never answers fails as a remote delete, in bounded time.
func TestRemoteDeleteTimesOut(t *testing.T) {
	r := newRig(t)
	r.svc.remoteTimeout = 50 * time.Millisecond
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	b := r.lastBackup(t, d.ID)
	remote.mu.Lock()
	remote.hang = true
	remote.mu.Unlock()
	errc := make(chan error, 1)
	go func() { errc <- r.svc.DeleteBackup(context.Background(), d.ID, b.ID) }()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrRemoteDelete) {
			t.Fatalf("err = %v, want ErrRemoteDelete", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DeleteBackup is still waiting on a bucket that does not answer")
	}
	if _, err := os.Stat(r.svc.filePath(d.ID, b)); err != nil {
		t.Fatalf("the file is gone: %v", err)
	}
}

// Review Focus 3: the uploaded object is the encrypted file, sent with its
// exact size, and no temp file stays behind.
func TestUploadEncrypts(t *testing.T) {
	const pass = "upload-passphrase-0123456789"
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.svc.d.Destinations = fakeOpener{remote: remote, dest: &backupdest.Destination{
		ID: 1, Name: "minio", Type: "s3", S3: backupdest.S3Config{Prefix: "pfx"}, Encrypt: true, Passphrase: pass}}
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	b := r.lastBackup(t, d.ID)
	if b.Upload == nil || b.Upload.Status != UploadUploaded || !b.Upload.Encrypted {
		t.Fatalf("upload = %+v", b.Upload)
	}
	key := backupdest.ObjectKey("pfx", "shop", d.ID, b.LogicalName, b.FileName) + ".age"
	if b.Upload.ObjectKey != key || !remote.has(key) {
		t.Fatalf("key = %q, bucket has %v", b.Upload.ObjectKey, remote.objects)
	}
	enc := remote.objects[key]
	if int64(len(enc)) != remote.sizes[key] {
		t.Fatalf("Put was told size %d, the encrypted object is %d", remote.sizes[key], len(enc))
	}
	if !agefile.IsAge(enc[:22]) {
		t.Fatalf("object does not start with the age header: %q", enc[:22])
	}
	rd, err := agefile.Decrypt(pass, bytes.NewReader(enc))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(rd)
	if err != nil {
		t.Fatal(err)
	}
	local, err := os.ReadFile(r.svc.filePath(d.ID, b))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, local) {
		t.Fatalf("decrypted %d bytes != local file %d bytes", len(plain), len(local))
	}
	tmps, _ := filepath.Glob(filepath.Join(r.svc.dbDir(d.ID), "backups", "*.age.tmp"))
	if len(tmps) != 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
}

func TestUploadEncryptsPutFailureLeavesNoTemp(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	remote.putErr = errors.New("connection refused")
	r.svc.d.Destinations = fakeOpener{remote: remote, dest: &backupdest.Destination{
		ID: 1, Name: "minio", Type: "s3", S3: backupdest.S3Config{Prefix: "pfx"}, Encrypt: true, Passphrase: "upload-passphrase-0123456789"}}
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	b := r.lastBackup(t, d.ID)
	if b.Upload == nil || b.Upload.Status != UploadFailed {
		t.Fatalf("upload = %+v", b.Upload)
	}
	tmps, _ := filepath.Glob(filepath.Join(r.svc.dbDir(d.ID), "backups", "*.age.tmp"))
	if len(tmps) != 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
}

func TestUploadPlainStaysPlain(t *testing.T) {
	r := newRig(t)
	d := r.create(t, "Shop", "postgres", "18")
	remote := r.withDestination(t)
	r.dumps("pg_dump -U postgres", "SELECT 1;\n")
	r.useClock(day(15, 3, 0))
	r.schedule(t, d.ID, r.toDestination, day(14, 3, 0))
	r.svc.tick(context.Background())
	b := r.lastBackup(t, d.ID)
	if b.Upload == nil || b.Upload.Status != UploadUploaded || b.Upload.Encrypted {
		t.Fatalf("upload = %+v", b.Upload)
	}
	if strings.HasSuffix(b.Upload.ObjectKey, ".age") || !remote.has(b.Upload.ObjectKey) {
		t.Fatalf("key = %q", b.Upload.ObjectKey)
	}
}

// Review: a restart during an encrypted upload must not strand the full-size
// temp file for good.
func TestStartupSweepsAgeTemps(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	d := r.create(t, "Shop", "postgres", "18")
	dir := filepath.Join(r.svc.dbDir(d.ID), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "f.sql.gz-123.age.tmp")
	keep := filepath.Join(dir, "f.sql.gz")
	for _, p := range []string{tmp, keep} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.svc.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("age temp survived Startup: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the Backup file is gone: %v", err)
	}
}

// A failure inside the encryption itself, not only in the put, leaves no
// temp file behind.
func TestEncryptToTempFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.sql.gz")
	if err := os.WriteFile(path, []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := encryptToTemp(path, ""); err == nil {
		t.Fatal("encrypting with an empty passphrase succeeded")
	}
	tmps, _ := filepath.Glob(filepath.Join(dir, "*.age.tmp"))
	if len(tmps) != 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
}
