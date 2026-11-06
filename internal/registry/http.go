package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
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
}

// API serves /api/registry. Every route is admin-only (authz leaves them
// out of Routes).
type API struct{ d APIDeps }

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
	disk, _ := a.d.Service.d.Host.VolumeBytes(ctx)
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
// tls or auth; "" is a pass.
func checkStep(errText string, code int) string {
	msg := strings.ToLower(errText)
	switch {
	case strings.Contains(msg, "x509") || strings.Contains(msg, "tls") || strings.Contains(msg, "certificate"):
		return "tls"
	case msg != "":
		return "dns"
	case code == http.StatusUnauthorized:
		return "auth"
	case code >= 200 && code < 300:
		return ""
	default:
		return "auth"
	}
}

// check requests https://<hostname>/v2/ from the panel with the system login,
// the way CI reaches the Registry.
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
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://"+st.Hostname+"/v2/", nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.SetBasicAuth(SystemUser, st.SystemSecret)
	code, errText := 0, ""
	if resp, err := a.d.CheckHTTP.Do(req); err != nil {
		errText = err.Error()
	} else {
		code = resp.StatusCode
		resp.Body.Close()
		if code < 200 || code > 299 {
			errText = ""
		}
	}
	step := checkStep(errText, code)
	message := errText
	if step == "auth" {
		message = "https://" + st.Hostname + "/v2/ answered " + strconv.Itoa(code) + " to the panel's login"
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": step == "", "step": step, "error": message})
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
	out := make([]map[string]any, 0, len(repos))
	for _, repo := range repos {
		tags, err := c.Tags(r.Context(), repo)
		if err != nil {
			writeError(w, http.StatusBadGateway, "could not reach the Registry: "+err.Error())
			return
		}
		out = append(out, map[string]any{"name": repo, "tagCount": len(tags)})
	}
	writeJSON(w, http.StatusOK, out)
}

type tagResponse struct {
	Tag     string    `json:"tag"`
	Digest  string    `json:"digest"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
	InUseBy []User    `json:"inUseBy"`
	Kept    bool      `json:"kept"`
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
	out := make([]tagResponse, 0, len(tags))
	for _, tag := range tags {
		info, err := c.TagInfo(ctx, repo, tag)
		if err != nil {
			return nil, err
		}
		base := hostname + "/" + repo
		in := mergeUsers(users[base+":"+tag], users[base+"@"+info.Digest])
		out = append(out, tagResponse{Tag: tag, Digest: info.Digest, Size: info.Size, Created: info.Created, InUseBy: in})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].Tag > out[j].Tag
	})
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
	if repo == "" {
		writeError(w, http.StatusBadRequest, "repo is required")
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
