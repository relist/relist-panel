package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"golang.org/x/sync/errgroup"
	"net"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/auth"
)

// User is a Compose that uses an image. A running container may name only
// the ids.
type User struct {
	ComposeID       int64  `json:"composeId"`
	ComposeName     string `json:"composeName"`
	ProjectID       int64  `json:"projectId"`
	ProjectName     string `json:"projectName"`
	EnvironmentID   int64  `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
}

// APIDeps wires the Registry page's API. DeployedRefs is each Compose's
// latest successful deploy (compose_image_refs); RunningRefs is what running
// containers hold. Both map an image reference to its users.
type APIDeps struct {
	Service      *Service
	RegistryBase string       // "" is InternalBase
	HTTP         *http.Client // for the registry API; nil is a 30s client
	CheckHTTP    *http.Client // for Check endpoint; nil is a 15s client
	DeployedRefs func(ctx context.Context) (map[string][]User, error)
	RunningRefs  func(ctx context.Context) (map[string][]User, error)
	ReadTimeout  func(ctx context.Context) (string, error)
	// Cleaner runs Registry cleanup; nil leaves the cleanup routes out.
	Cleaner *Cleaner
}

// API serves /api/registry. Every route is admin-only (authz leaves them
// out of Routes).
type API struct {
	d APIDeps

	// infos caches tag info by repo@manifest-digest: a manifest never
	// changes, so a later load only HEADs each tag for its digest.
	infoMu sync.Mutex
	infos  map[string]TagInfo
}

// maxCachedInfos bounds the tag info cache; past it the cache starts over.
const maxCachedInfos = 10000

// The registry's name grammar (distribution/reference): lowercase path
// components joined by "/", and tags of up to 128 word characters, "." or
// "-", not starting with "." or "-".
var (
	repoName = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	tagName  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// checkRepoTag answers 400 and false when repo, or tag when wantTag, does
// not follow the registry's grammar.
func checkRepoTag(w http.ResponseWriter, repo, tag string, wantTag bool) bool {
	if !repoName.MatchString(repo) || len(repo) > 255 {
		writeError(w, http.StatusBadRequest, "repo must be a repository name such as app or team/web")
		return false
	}
	if wantTag && !tagName.MatchString(tag) {
		writeError(w, http.StatusBadRequest, "tag must be up to 128 letters, digits, _, . or -, not starting with . or -")
		return false
	}
	return true
}

// tagInfo reads a tag, from the cache when its manifest digest was read
// before.
func (a *API) tagInfo(ctx context.Context, c *Client, repo, tag string) (TagInfo, error) {
	digest, err := c.Digest(ctx, repo, tag)
	if err != nil {
		return TagInfo{}, err
	}
	if digest == "" { // no Docker-Content-Digest: nothing to key the cache by
		return c.TagInfo(ctx, repo, tag)
	}
	key := repo + "@" + digest
	a.infoMu.Lock()
	info, ok := a.infos[key]
	a.infoMu.Unlock()
	if ok {
		info.Tag = tag
		return info, nil
	}
	if info, err = c.TagInfo(ctx, repo, tag); err != nil {
		return TagInfo{}, err
	}
	// A created time that could not be read (its config blob failed) is
	// read again next time instead of shown as unknown for good.
	if info.Created.IsZero() || info.Digest == "" {
		return info, nil
	}
	a.infoMu.Lock()
	if a.infos == nil || len(a.infos) >= maxCachedInfos {
		a.infos = map[string]TagInfo{}
	}
	a.infos[repo+"@"+info.Digest] = info
	a.infoMu.Unlock()
	return info, nil
}

// tagInfos reads every tag of repo, eight at a time, in tags' order.
func (a *API) tagInfos(ctx context.Context, c *Client, repo string, tags []string) ([]TagInfo, error) {
	out := make([]TagInfo, len(tags))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i, tag := range tags {
		g.Go(func() error {
			info, err := a.tagInfo(ctx, c, repo, tag)
			out[i] = info
			return err
		})
	}
	return out, g.Wait()
}

// newest is the latest created time among repo's tags, or nil when none can
// be read. A tag that fails is left out: it only decides a date.
func (a *API) newest(ctx context.Context, c *Client, repo string, tags []string) *time.Time {
	times := make([]time.Time, len(tags))
	var g errgroup.Group
	g.SetLimit(8)
	for i, tag := range tags {
		g.Go(func() error {
			if info, err := a.tagInfo(ctx, c, repo, tag); err == nil {
				times[i] = info.Created
			}
			return nil
		})
	}
	_ = g.Wait()
	var newest *time.Time
	for _, at := range times {
		if !at.IsZero() && (newest == nil || at.After(*newest)) {
			newest = &at
		}
	}
	return newest
}

func NewAPI(d APIDeps) *API {
	if d.RegistryBase == "" {
		d.RegistryBase = InternalBase
	}
	if d.CheckHTTP == nil {
		d.CheckHTTP = &http.Client{Timeout: 15 * time.Second}
	}
	return &API{d: d}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/registry", a.get)
	mux.HandleFunc("PUT /api/registry", a.put)
	mux.HandleFunc("POST /api/registry/enable", a.enable)
	mux.HandleFunc("POST /api/registry/disable", a.disable)
	mux.HandleFunc("POST /api/registry/repair", a.repair)
	mux.HandleFunc("POST /api/registry/check", a.check)
	mux.HandleFunc("DELETE /api/registry/data", a.deleteData)
	mux.HandleFunc("GET /api/registry/credentials", a.listCredentials)
	mux.HandleFunc("POST /api/registry/credentials", a.createCredential)
	mux.HandleFunc("DELETE /api/registry/credentials/{id}", a.deleteCredential)
	mux.HandleFunc("GET /api/registry/repositories", a.repositories)
	mux.HandleFunc("GET /api/registry/tags", a.tags)
	mux.HandleFunc("DELETE /api/registry/tags", a.deleteTag)
	if a.d.Cleaner != nil {
		mux.HandleFunc("GET /api/registry/cleanup", a.getCleanup)
		mux.HandleFunc("PUT /api/registry/cleanup", a.putCleanup)
		mux.HandleFunc("GET /api/registry/cleanup/preview", a.previewCleanup)
		mux.HandleFunc("POST /api/registry/cleanup/run", a.runCleanup)
	}
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := a.d.Service.d.Store.Settings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the Registry settings")
		return
	}
	status, err := a.d.Service.Status(ctx)
	if err != nil {
		status = StatusError
	}
	disk := a.d.Service.cachedVolumeBytes(ctx)
	timeout := ""
	if a.d.ReadTimeout != nil {
		timeout, _ = a.d.ReadTimeout(ctx)
	}
	using, err := a.d.Service.ComposesUsing(ctx)
	if err != nil || using == nil {
		using = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":      map[string]any{"enabled": st.Enabled, "hostname": st.Hostname, "tls": st.TLS},
		"status":        status,
		"lastError":     st.LastError,
		"diskBytes":     disk,
		"readTimeout":   timeout,
		"composesUsing": using,
	})
}

func (a *API) put(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hostname string `json:"hostname"`
		TLS      string `json:"tls"`
	}
	if !decode(w, r, &in) {
		return
	}
	audit.Target(r.Context(), strings.TrimSpace(in.Hostname))
	if err := a.d.Service.Save(r.Context(), Settings{Hostname: in.Hostname, TLS: in.TLS}); err != nil {
		writeServiceError(w, err)
		return
	}
	a.get(w, r)
}

func (a *API) enable(w http.ResponseWriter, r *http.Request) {
	if err := a.d.Service.Enable(r.Context()); err != nil {
		writeServiceError(w, err)
		return
	}
	a.get(w, r)
}

func (a *API) disable(w http.ResponseWriter, r *http.Request) {
	warnings, err := a.d.Service.Disable(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"warnings": warnings})
}

func (a *API) repair(w http.ResponseWriter, r *http.Request) {
	if _, err := a.d.Service.Reconcile(r.Context()); err != nil {
		writeServiceError(w, err)
		return
	}
	a.get(w, r)
}

// checkStep names where a request to https://<hostname>/v2/ failed: dns,
// connect, tls, auth (the login was refused) or http (something other than
// the Registry answered); "" is a pass.
func checkStep(err error, code int) string {
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr):
		return "dns"
	case err != nil && (strings.Contains(err.Error(), "x509") || strings.Contains(err.Error(), "tls") || strings.Contains(err.Error(), "certificate")):
		return "tls"
	case err != nil:
		return "connect"
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return "auth"
	case code >= 200 && code < 300:
		return ""
	default:
		return "http"
	}
}

// registryRealm is the htpasswd realm config.yml sets; only a host asking
// for it gets the system login.
const registryRealm = `realm="relist-registry"`

// check requests https://<hostname>/v2/ from the panel, the way CI reaches
// the Registry. It asks without a login first and sends the system login only
// to a host that asks for this Registry's realm, so a mistyped hostname never
// receives it.
func (a *API) check(w http.ResponseWriter, r *http.Request) {
	st, err := a.d.Service.d.Store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the Registry settings")
		return
	}
	if !st.Enabled {
		writeError(w, http.StatusConflict, "the Registry is off")
		return
	}
	url := "https://" + st.Hostname + "/v2/"
	get := func(login bool) (*http.Response, error) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if login {
			req.SetBasicAuth(SystemUser, st.SystemSecret)
		}
		resp, err := a.d.CheckHTTP.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		return resp, err
	}
	report := func(err error, code int, message string) {
		step := checkStep(err, code)
		if err != nil {
			message = err.Error()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": step == "", "step": step, "error": message})
	}
	resp, err := get(false)
	if err != nil {
		report(err, 0, "")
		return
	}
	if resp.StatusCode != http.StatusUnauthorized {
		report(nil, http.StatusTeapot, url+" answered "+strconv.Itoa(resp.StatusCode)+" without asking for a login, so it is not this Registry")
		return
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), registryRealm) {
		report(nil, http.StatusUnauthorized, url+" asked for another registry's login, so the panel did not send its own")
		return
	}
	if resp, err = get(true); err != nil {
		report(err, 0, "")
		return
	}
	report(nil, resp.StatusCode, url+" answered "+strconv.Itoa(resp.StatusCode)+" to the panel's login")
}

func (a *API) deleteData(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.d.Service.DeleteData(r.Context(), in.Confirm); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type credentialResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

func toCredential(c Credential) credentialResponse {
	return credentialResponse{ID: c.ID, Name: c.Name, CreatedBy: c.CreatedBy, CreatedAt: c.CreatedAt}
}

func (a *API) listCredentials(w http.ResponseWriter, r *http.Request) {
	list, err := a.d.Service.d.Store.Credentials(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list Push credentials")
		return
	}
	out := make([]credentialResponse, 0, len(list))
	for _, c := range list {
		out = append(out, toCredential(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createCredential(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	audit.Target(r.Context(), name)
	by := ""
	if u, ok := auth.UserFrom(r.Context()); ok {
		by = u.Email
	}
	c, token, err := a.d.Service.CreateCredential(r.Context(), name, by)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"credential": toCredential(c), "token": token})
}

func (a *API) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	c, err := a.d.Service.DeleteCredential(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	audit.Target(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}

// client is the registry API with the system login; ok is false (and the
// answer written) when the Registry is off.
func (a *API) client(w http.ResponseWriter, r *http.Request) (*Client, Settings, bool) {
	st, err := a.d.Service.d.Store.Settings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the Registry settings")
		return nil, st, false
	}
	if !st.Enabled {
		writeError(w, http.StatusConflict, "Registry is off")
		return nil, st, false
	}
	// Spec: before every Registry operation, repair drift (a cheap Inspect
	// when there is none). A failure shows up as the call's own error.
	_, _ = a.d.Service.Reconcile(r.Context())
	return &Client{Base: a.d.RegistryBase, User: SystemUser, Secret: st.SystemSecret, HTTP: a.d.HTTP}, st, true
}

func (a *API) repositories(w http.ResponseWriter, r *http.Request) {
	c, _, ok := a.client(w, r)
	if !ok {
		return
	}
	repos, err := c.Repositories(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not reach the Registry: "+err.Error())
		return
	}
	type repoResponse struct {
		Name     string     `json:"name"`
		TagCount int        `json:"tagCount"`
		Newest   *time.Time `json:"newest"`
	}
	out := make([]repoResponse, 0, len(repos))
	for _, repo := range repos {
		tags, err := c.Tags(r.Context(), repo)
		if err != nil {
			writeError(w, http.StatusBadGateway, "could not reach the Registry: "+err.Error())
			return
		}
		out = append(out, repoResponse{Name: repo, TagCount: len(tags), Newest: a.newest(r.Context(), c, repo, tags)})
	}
	writeJSON(w, http.StatusOK, out)
}

type tagResponse struct {
	Tag    string `json:"tag"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	// Created is null when the image config could not be read.
	Created *time.Time `json:"created"`
	InUseBy []User     `json:"inUseBy"`
	Kept    bool       `json:"kept"`
}

func toTagResponse(info TagInfo, users []User) tagResponse {
	r := tagResponse{Tag: info.Tag, Digest: info.Digest, Size: info.Size, InUseBy: users}
	if !info.Created.IsZero() {
		created := info.Created
		r.Created = &created
	}
	return r
}

func createdOf(t tagResponse) time.Time {
	if t.Created == nil {
		return time.Time{}
	}
	return *t.Created
}

// repoTags reads every tag of repo with its users, newest first.
func (a *API) repoTags(ctx context.Context, c *Client, hostname, repo string) ([]tagResponse, error) {
	tags, err := c.Tags(ctx, repo)
	if err != nil {
		return nil, err
	}
	users, err := a.users(ctx)
	if err != nil {
		return nil, err
	}
	infos, err := a.tagInfos(ctx, c, repo, tags)
	if err != nil {
		return nil, err
	}
	out := make([]tagResponse, 0, len(infos))
	base := hostname + "/" + repo
	for _, info := range infos {
		out = append(out, toTagResponse(info, mergeUsers(users[base+":"+info.Tag], users[base+"@"+info.Digest])))
	}
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := createdOf(out[i]), createdOf(out[j])
		if !ci.Equal(cj) {
			return ci.After(cj)
		}
		return out[i].Tag > out[j].Tag
	})
	// Kept: what cleanup would keep with today's settings.
	if settings, err := a.d.Service.d.Store.CleanupSettings(ctx); err == nil {
		used := map[string]bool{}
		for _, t := range out {
			used[t.Tag] = len(t.InUseBy) > 0
		}
		_, gone := SelectDeletions(infos, used, settings.Keep)
		for i := range out {
			out[i].Kept = !slices.Contains(gone, out[i].Tag)
		}
	}
	return out, nil
}

// users merges both in-use sources by reference.
func (a *API) users(ctx context.Context) (map[string][]User, error) {
	out := map[string][]User{}
	for _, source := range []func(context.Context) (map[string][]User, error){a.d.DeployedRefs, a.d.RunningRefs} {
		if source == nil {
			continue
		}
		refs, err := source(ctx)
		if err != nil {
			return nil, err
		}
		for ref, us := range refs {
			out[ref] = mergeUsers(out[ref], us)
		}
	}
	return out, nil
}

// mergeUsers is a and b without a Compose twice; a named entry wins over a
// bare id.
func mergeUsers(a, b []User) []User {
	out := []User{}
	for _, u := range slices.Concat(a, b) {
		i := slices.IndexFunc(out, func(o User) bool { return o.ComposeID == u.ComposeID })
		switch {
		case i < 0:
			out = append(out, u)
		case out[i].ComposeName == "" && u.ComposeName != "":
			out[i] = u
		}
	}
	return out
}

func (a *API) tags(w http.ResponseWriter, r *http.Request) {
	c, st, ok := a.client(w, r)
	if !ok {
		return
	}
	repo := r.URL.Query().Get("repo")
	if !checkRepoTag(w, repo, "", false) {
		return
	}
	out, err := a.repoTags(r.Context(), c, st.Hostname, repo)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not reach the Registry: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteTag deletes the manifest a tag points at, so every tag sharing its
// digest goes too. One of them in use needs {"confirm": "<tag>"}.
func (a *API) deleteTag(w http.ResponseWriter, r *http.Request) {
	c, st, ok := a.client(w, r)
	if !ok {
		return
	}
	repo, tag := r.URL.Query().Get("repo"), r.URL.Query().Get("tag")
	if !checkRepoTag(w, repo, tag, true) {
		return
	}
	var in struct {
		Confirm string `json:"confirm"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	audit.Target(r.Context(), repo+":"+tag)
	tags, err := a.repoTags(r.Context(), c, st.Hostname, repo)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not reach the Registry: "+err.Error())
		return
	}
	i := slices.IndexFunc(tags, func(t tagResponse) bool { return t.Tag == tag })
	if i < 0 {
		writeError(w, http.StatusNotFound, "no such tag")
		return
	}
	digest := tags[i].Digest
	var deleted []string
	var users []User
	for _, t := range tags {
		if t.Digest == digest {
			deleted = append(deleted, t.Tag)
			users = mergeUsers(users, t.InUseBy)
		}
	}
	if len(users) > 0 && in.Confirm != tag {
		names := make([]string, 0, len(users))
		for _, u := range users {
			name := u.ComposeName
			if name == "" {
				name = "Compose " + strconv.FormatInt(u.ComposeID, 10)
			}
			names = append(names, name)
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": "In use by " + strings.Join(names, ", ") + "; type the tag to delete it anyway", "inUseBy": users})
		return
	}
	if err := c.Delete(r.Context(), repo, digest); err != nil {
		writeError(w, http.StatusBadGateway, "could not delete: "+err.Error())
		return
	}
	sort.Strings(deleted)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	var conflict *ErrHostnameConflict
	switch {
	case errors.As(err, &conflict), errors.Is(err, ErrDuplicateName), errors.Is(err, ErrStillEnabled):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrInvalidHostname), errors.Is(err, ErrInvalidTLS), errors.Is(err, ErrNoHostname),
		errors.Is(err, ErrConfirm), errors.Is(err, ErrInvalidName):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// inUseRefs is every reference users() knows, for retention.
func (a *API) inUseRefs(ctx context.Context) (map[string]bool, error) {
	users, err := a.users(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(users))
	for ref, us := range users {
		out[ref] = len(us) > 0
	}
	return out, nil
}

func (a *API) writeCleanup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := a.d.Service.d.Store.CleanupSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the cleanup settings")
		return
	}
	runs, err := a.d.Service.d.Store.Runs(ctx, keptRuns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the cleanup history")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": settings,
		"next":     Next(a.d.Cleaner.d.Now(), settings),
		"running":  a.d.Cleaner.Running(),
		"runs":     runs,
	})
}

func (a *API) getCleanup(w http.ResponseWriter, r *http.Request) { a.writeCleanup(w, r) }

var weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

func (a *API) putCleanup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool     `json:"enabled"`
		Keep    int      `json:"keep"`
		Days    []string `json:"days"`
		Minute  int      `json:"minute"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch {
	case in.Keep < 1:
		writeError(w, http.StatusUnprocessableEntity, "keep at least 1 tag per repository")
		return
	case in.Minute < 0 || in.Minute > 1439:
		writeError(w, http.StatusUnprocessableEntity, "the time must be between 00:00 and 23:59")
		return
	case len(in.Days) == 0:
		writeError(w, http.StatusUnprocessableEntity, "choose Daily or at least one weekday")
		return
	}
	days := []string{}
	for _, d := range in.Days {
		switch {
		case d == "daily":
			days = []string{"daily"}
		case slices.Contains(weekdays, d):
			if !slices.Contains(days, d) && !slices.Contains(days, "daily") {
				days = append(days, d)
			}
		default:
			writeError(w, http.StatusUnprocessableEntity, "days must be daily or weekdays mon to sun")
			return
		}
	}
	slices.SortFunc(days, func(x, y string) int { return slices.Index(weekdays, x) - slices.Index(weekdays, y) })
	ctx := r.Context()
	settings, err := a.d.Service.d.Store.CleanupSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the cleanup settings")
		return
	}
	if in.Enabled && !settings.Enabled {
		// Switched on: the schedule starts from now, not with a run at once.
		now := a.d.Cleaner.d.Now()
		settings.LastRunAt = &now
	}
	settings.Enabled, settings.Keep, settings.Days, settings.Minute = in.Enabled, in.Keep, days, in.Minute
	if err := a.d.Service.d.Store.SaveCleanupSettings(ctx, settings); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the cleanup settings")
		return
	}
	a.writeCleanup(w, r)
}

func (a *API) previewCleanup(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.client(w, r); !ok {
		return
	}
	preview, err := a.d.Cleaner.preview(r.Context(), a.inUseRefs)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not reach the Registry: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (a *API) runCleanup(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.client(w, r); !ok {
		return
	}
	trigger := "user"
	if u, ok := auth.UserFrom(r.Context()); ok {
		trigger = "user:" + u.Email
	}
	id, err := a.d.Cleaner.RunNow(r.Context(), trigger)
	if errors.Is(err, ErrCleanupBusy) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"runId": id})
}
