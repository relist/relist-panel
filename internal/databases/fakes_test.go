package databases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"relist-panel/internal/dbquery"
)

// fakeHost is an in-memory Host. It records every call as a string, keeps
// containers and volumes in maps and fails the calls named in failOn.
type fakeHost struct {
	mu         sync.Mutex
	calls      []string
	containers map[string]*ContainerState
	specs      map[string]ContainerSpec
	volumes    map[string]bool
	images     map[string]string // ref -> image ID after a pull
	failOn     map[string]error  // call prefix -> error
	logs       string
	// onCreate, when set, runs before each Create (outside the lock), so a
	// test can hold a recreate half-way.
	onCreate func()

	// Exec scripts: the first script whose prefix starts the joined command
	// answers; an unscripted command exits 0 with no output.
	scripts  []execScript
	execReqs []ExecRequest
}

type execScript struct {
	prefix string
	fn     func(req ExecRequest) (int, error)
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		containers: map[string]*ContainerState{},
		specs:      map[string]ContainerSpec{},
		volumes:    map[string]bool{},
		images:     map[string]string{},
		failOn:     map[string]error{},
	}
}

func (f *fakeHost) record(call string) error {
	f.calls = append(f.calls, call)
	for prefix, err := range f.failOn {
		if strings.HasPrefix(call, prefix) {
			return err
		}
	}
	return nil
}

// callsExceptInspect is the call log without read-only calls.
func (f *fakeHost) mutating() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "inspect ") && !strings.HasPrefix(c, "volume-exists ") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeHost) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeHost) saw(call string) bool { return slices.Contains(f.all(), call) }

func (f *fakeHost) count(prefix string) int {
	n := 0
	for _, c := range f.all() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeHost) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func (f *fakeHost) EnsureImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record("ensure " + ref)
}

func (f *fakeHost) PullImage(_ context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("pull " + ref); err != nil {
		return "", err
	}
	id := f.images[ref]
	if id == "" {
		id = "sha256:img-" + ref
	}
	return id, nil
}

func (f *fakeHost) ImageID(_ context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return "sha256:img-" + ref, nil
}

func (f *fakeHost) Inspect(_ context.Context, name string) (ContainerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("inspect " + name); err != nil {
		return ContainerState{}, err
	}
	if c, ok := f.containers[name]; ok {
		return *c, nil
	}
	return ContainerState{}, nil
}

func (f *fakeHost) VolumeExists(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("volume-exists " + name); err != nil {
		return false, err
	}
	return f.volumes[name], nil
}

func (f *fakeHost) Create(_ context.Context, spec ContainerSpec) error {
	f.mu.Lock()
	hook := f.onCreate
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("create " + spec.Name); err != nil {
		return err
	}
	f.volumes[spec.Volume] = true
	f.specs[spec.Name] = spec
	st := &ContainerState{Exists: true, Running: true, Health: "healthy", Hash: spec.Labels[HashLabel], ImageID: "sha256:img-" + spec.Image}
	if id, ok := f.images[spec.Image]; ok {
		st.ImageID = id
	}
	if spec.Alias != "" {
		st.Aliases = []string{spec.Alias}
	}
	f.containers[spec.Name] = st
	return nil
}

func (f *fakeHost) with(name, call string, fn func(*ContainerState)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(call + " " + name); err != nil {
		return err
	}
	c, ok := f.containers[name]
	if !ok {
		return errors.New("docker: no such container " + name)
	}
	fn(c)
	return nil
}

func (f *fakeHost) Start(_ context.Context, name string) error {
	return f.with(name, "start", func(c *ContainerState) { c.Running = true })
}
func (f *fakeHost) Stop(_ context.Context, name string) error {
	return f.with(name, "stop", func(c *ContainerState) { c.Running = false })
}
func (f *fakeHost) Restart(_ context.Context, name string) error {
	return f.with(name, "restart", func(c *ContainerState) { c.Running = true })
}
func (f *fakeHost) Realias(_ context.Context, name, alias string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(fmt.Sprintf("realias %s %s", name, alias)); err != nil {
		return err
	}
	c, ok := f.containers[name]
	if !ok {
		return errors.New("docker: no such container " + name)
	}
	c.Aliases = nil
	if alias != "" {
		c.Aliases = []string{alias}
	}
	return nil
}

func (f *fakeHost) Remove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("remove " + name); err != nil {
		return err
	}
	delete(f.containers, name)
	return nil
}

func (f *fakeHost) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("remove-volume " + name); err != nil {
		return err
	}
	delete(f.volumes, name)
	return nil
}

func (f *fakeHost) Logs(_ context.Context, name string, tail int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record(fmt.Sprintf("logs %s %d", name, tail)); err != nil {
		return "", err
	}
	return f.logs, nil
}

// script answers every Exec whose command line starts with prefix.
func (f *fakeHost) script(prefix string, fn func(req ExecRequest) (int, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = append(f.scripts, execScript{prefix, fn})
}

// execs returns the recorded Exec requests whose command starts with prefix.
func (f *fakeHost) execs(prefix string) []ExecRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ExecRequest
	for _, r := range f.execReqs {
		if strings.HasPrefix(strings.Join(r.Cmd, " "), prefix) {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeHost) Exec(_ context.Context, name string, req ExecRequest) (int, error) {
	line := strings.Join(req.Cmd, " ")
	f.mu.Lock()
	err := f.record("exec " + name + " " + line)
	f.execReqs = append(f.execReqs, req)
	var fn func(ExecRequest) (int, error)
	for _, sc := range f.scripts {
		if strings.HasPrefix(line, sc.prefix) {
			fn = sc.fn
			break
		}
	}
	f.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if fn == nil {
		// An unscripted list answers one database, "app", so a test that does
		// not care which databases exist still has one to back up.
		if strings.HasPrefix(line, "psql -U postgres -At -c SELECT datname") || strings.HasPrefix(line, "mysql -uroot -N -e SHOW DATABASES") {
			_, err := io.WriteString(req.Stdout, "app\n")
			return 0, err
		}
		return 0, nil
	}
	return fn(req)
}

// adminCall is one Exec on the fake Admin.
type adminCall struct {
	Database string
	Stmts    []string
}

// fakeAdmin is an in-memory Admin. It records every Exec, answers Rows from a
// map (keyed "<database>\x00<query>", then "<query>"), and answers Ping by
// the password it was given.
type fakeAdmin struct {
	mu    sync.Mutex
	execs []adminCall
	rows  map[string][][]string
	// execErr, when set, decides each Exec's error before it is recorded as
	// done; the call is recorded either way.
	execErr func(database string, stmts []string) error
	// pingOK says which passwords log in; any other gets pingDefault.
	pingOK      map[string]bool
	pingDefault bool
	pings       []string
	redisSet    []string
	redisSetErr error
	targets     []dbquery.Target
}

func newFakeAdmin() *fakeAdmin {
	return &fakeAdmin{rows: map[string][][]string{}, pingOK: map[string]bool{}}
}

func (f *fakeAdmin) Exec(_ context.Context, t dbquery.Target, _, database string, stmts []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execs = append(f.execs, adminCall{database, slices.Clone(stmts)})
	f.targets = append(f.targets, t)
	if f.execErr != nil {
		return f.execErr(database, stmts)
	}
	return nil
}

func (f *fakeAdmin) Rows(_ context.Context, _ dbquery.Target, _, database, query string) ([][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[database+"\x00"+query]; ok {
		return r, nil
	}
	return f.rows[query], nil
}

func (f *fakeAdmin) Ping(_ context.Context, t dbquery.Target, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pings = append(f.pings, t.Password)
	if ok, known := f.pingOK[t.Password]; (known && !ok) || (!known && !f.pingDefault) {
		return errors.New("password authentication failed")
	}
	return nil
}

func (f *fakeAdmin) SetRedisPassword(_ context.Context, _ dbquery.Target, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redisSet = append(f.redisSet, password)
	return f.redisSetErr
}

// calls returns the recorded Execs.
func (f *fakeAdmin) calls() []adminCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.execs)
}

// stmts is every executed statement in order, without the database.
func (f *fakeAdmin) stmts() []string {
	var out []string
	for _, c := range f.calls() {
		out = append(out, c.Stmts...)
	}
	return out
}

// answer sets the rows a query returns on every database.
func (f *fakeAdmin) answer(query string, rows [][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[query] = rows
}

// answerOn sets the rows a query returns on one database.
func (f *fakeAdmin) answerOn(database, query string, rows [][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[database+"\x00"+query] = rows
}
