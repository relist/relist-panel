package deployments

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
)

func dirSize(t *testing.T, root string) (n int64) {
	filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}

// C1 + B3: successive zip-Source deployments each leave a full copy of src/
// under .deployment-sources, and the running stack's ./src bind mount points
// at that copy, so what a container writes there is not carried forward.
func TestReviewC1B3SnapshotDirs(t *testing.T) {
	db, id, _ := newDB(t)
	runner := &snapshotRunner{}
	content := "services:\n  web:\n    image: sample\n    volumes: ['./src:/app']\n"
	if _, err := db.Exec(`UPDATE composes SET yaml_content=?, identity_version=1 WHERE id=?`, content, id); err != nil {
		t.Fatal(err)
	}
	svc := composes.New(db, runner, t.TempDir())
	target, _ := svc.Target(context.Background(), id)
	dir := composes.ComposeDir(target.Request)
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "app.txt"), []byte("code"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "bundle.bin"), bytes.Repeat([]byte{1}, 5<<20), 0o644) // 5 MiB of "code"
	srcStore := sources.NewStore(db)
	srcStore.Save(context.Background(), sources.Source{ComposeID: id, Kind: sources.KindZip, ZipName: "site.zip"})
	q := NewQueue(Deps{Store: NewStore(db), Sources: srcStore, Composes: svc, Fetcher: &countedGit{}, Builder: runner, Gate: &fakeGate{rec: &recorder{}}, Keep: 2})

	for i := 1; i <= 6; i++ {
		d, _, err := q.Enqueue(context.Background(), id, Request{Mode: ModeSkipBuild})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.process(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		got, _ := q.d.Store.Get(context.Background(), id, d.Number)
		if i == 1 {
			// B3: the container "writes" into its ./src mount.
			mounted := runner.mountedPaths[len(runner.mountedPaths)-1]
			t.Logf("B3 deploy 1 mounts ./src from %s", strings.TrimPrefix(mounted, dir))
			os.WriteFile(filepath.Join(mounted, "uploads.db"), []byte("user data"), 0o644)
		}
		if i == 2 {
			mounted := runner.mountedPaths[len(runner.mountedPaths)-1]
			_, err := os.Stat(filepath.Join(mounted, "uploads.db"))
			t.Logf("B3 deploy 2 mounts %s; uploads.db written by deploy 1's container present: %v", strings.TrimPrefix(mounted, dir), err == nil)
		}
		entries, _ := os.ReadDir(filepath.Join(dir, sourceSnapshotsDir))
		var rows int
		db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE compose_id=?`, id).Scan(&rows)
		t.Logf("C1 after deploy %d (%s): history rows=%d (Keep=2), snapshot dirs=%d, size=%.1f MiB", i, got.Status, rows, len(entries), float64(dirSize(t, filepath.Join(dir, sourceSnapshotsDir)))/(1<<20))
	}
}

// B2: a queued Deployment with FreshVolumes absorbs a later plain request.
func TestReviewB2DedupKeepsFreshVolumes(t *testing.T) {
	h := newHarness(t)
	h.newQueue()
	first, created1, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy, FreshVolumes: true})
	if err != nil {
		t.Fatal(err)
	}
	second, created2, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy, FreshVolumes: false})
	if err != nil {
		t.Fatal(err)
	}
	stored := h.get(h.a, second.Number)
	t.Logf("B2 fresh request -> #%d created=%v; plain request -> #%d created=%v; the queued row has FreshVolumes=%v", first.Number, created1, second.Number, created2, stored.FreshVolumes)

	third, created3, _ := h.q.Enqueue(context.Background(), h.b, Request{Mode: ModeDeploy})
	fourth, created4, _ := h.q.Enqueue(context.Background(), h.b, Request{Mode: ModeDeploy, FreshVolumes: true})
	t.Logf("B2 reverse: plain #%d created=%v; fresh #%d created=%v; queued FreshVolumes=%v", third.Number, created3, fourth.Number, created4, h.get(h.b, fourth.Number).FreshVolumes)
}
