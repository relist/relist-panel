package volumeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"relist-panel/internal/auth"
	"relist-panel/internal/chunkupload"
	"relist-panel/internal/volumes"
)

const (
	// uploadPurpose is the chunkupload Purpose a Volume upload registers.
	uploadPurpose = "volume"
	// UploadCapChunked is a chunked Volume upload's cap; the direct write
	// keeps its own 1 GiB.
	UploadCapChunked = 10 << 30
	// completeWait is how long complete waits for the write before it
	// answers 202 and leaves the client to poll: far under Cloudflare's 125 s.
	completeWait = 10 * time.Second
)

// UploadPurpose is what the panel registers with chunkupload for Volumes.
var UploadPurpose = chunkupload.Purpose{Name: uploadPurpose, Cap: UploadCapChunked}

// VolumeLocker is the Project coordination a background write takes: the
// mutation gate first, then the exclusive volume lock, as a Deployment does.
type VolumeLocker interface {
	Acquire(ctx context.Context, projectID int64) (func(), error)
	AcquireVolumes(ctx context.Context, projectID int64) (func(), error)
}

// uploadJSON is one upload as the API shows it. source and path come back
// out of the stored query; offset is what the panel holds.
type uploadJSON struct {
	ID           string    `json:"id"`
	Source       string    `json:"source"`
	Path         string    `json:"path"`
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	LastModified int64     `json:"lastModified"`
	Offset       int64     `json:"offset"`
	State        string    `json:"state"`
	ErrorKind    string    `json:"errorKind"`
	Error        string    `json:"error"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func uploadView(s chunkupload.Session) uploadJSON {
	query, _ := url.ParseQuery(s.Target)
	return uploadJSON{
		ID: s.ID, Source: query.Get("source"), Path: query.Get("path"), Name: s.Name, Size: s.Size,
		LastModified: s.LastModified, Offset: s.Received, State: string(s.State),
		ErrorKind: string(s.ErrorKind), Error: s.Error, UpdatedAt: s.UpdatedAt.UTC(),
	}
}

// uploadCaller is the signed-in User and the Compose in the URL: every upload
// lookup is scoped to both.
func (s *Service) uploadCaller(w http.ResponseWriter, r *http.Request) (userID, composeID int64, ok bool) {
	if s.uploads == nil {
		writeError(w, http.StatusServiceUnavailable, "chunked uploads are unavailable: the staging directory could not be opened")
		return 0, 0, false
	}
	user, signedIn := auth.UserFrom(r.Context())
	if !signedIn {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return 0, 0, false
	}
	composeID, err := parsePositiveID(r.PathValue("composeID"))
	if err != nil {
		writeError(w, http.StatusNotFound, "compose not found")
		return 0, 0, false
	}
	return user.ID, composeID, true
}

// startUpload runs the direct write's own checks against the query, then
// asks the Volume for its free space: the staged copy and the helper's temp
// file exist together while the file is written, so it needs twice the size.
func (s *Service) startUpload(w http.ResponseWriter, r *http.Request) {
	userID, composeID, ok := s.uploadCaller(w, r)
	if !ok {
		return
	}
	req, _, ok := s.contentOperation(w, r, volumes.OpWrite)
	if !ok {
		return
	}
	var body struct {
		Name         string `json:"name"`
		Size         int64  `json:"size"`
		LastModified int64  `json:"lastModified"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}
	if body.Size < 1 {
		writeError(w, http.StatusBadRequest, "upload size must be at least 1 byte")
		return
	}
	if body.Size > UploadCapChunked {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("upload exceeds the %d GiB cap", UploadCapChunked>>30))
		return
	}

	free, err := s.volumeFree(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "volume free space check failed: "+err.Error())
		return
	}
	if need := 2 * body.Size; free < need {
		writeError(w, http.StatusInsufficientStorage, fmt.Sprintf(
			"the Volume needs %d bytes free to write a %d-byte upload (the staged copy and the written file), and has %d", need, body.Size, free))
		return
	}

	session, err := s.uploads.Create(r.Context(), chunkupload.NewSession{
		Purpose: uploadPurpose, UserID: userID, ComposeID: composeID, Target: r.URL.RawQuery,
		Name: body.Name, Size: body.Size, LastModified: body.LastModified,
	})
	var noSpace *chunkupload.NoSpaceError
	switch {
	case errors.Is(err, chunkupload.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("upload exceeds the %d GiB cap", UploadCapChunked>>30))
		return
	case errors.Is(err, chunkupload.ErrTooManyOpen):
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	case errors.As(err, &noSpace):
		writeError(w, http.StatusInsufficientStorage, "the panel's disk cannot stage this upload: "+noSpace.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not start the upload: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": session.ID, "chunkSize": chunkupload.ChunkSize, "offset": 0})
}

// volumeFree runs the helper's free op, read-only, as the write's user.
func (s *Service) volumeFree(ctx context.Context, write volumes.Request) (int64, error) {
	var out bytes.Buffer
	req := volumes.Request{Source: write.Source, User: write.User, Op: volumes.OpFree, Path: ".", ReadOnly: true}
	if err := s.runner.Run(ctx, req, nil, &out); err != nil {
		return 0, err
	}
	var free struct {
		Bytes int64 `json:"bytes"`
	}
	if err := json.Unmarshal(out.Bytes(), &free); err != nil {
		return 0, fmt.Errorf("parse free output: %w", err)
	}
	return free.Bytes, nil
}

func (s *Service) listUploads(w http.ResponseWriter, r *http.Request) {
	userID, composeID, ok := s.uploadCaller(w, r)
	if !ok {
		return
	}
	sessions, err := s.uploads.ListOpen(r.Context(), userID, composeID, uploadPurpose)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]uploadJSON, 0, len(sessions))
	for _, session := range sessions {
		views = append(views, uploadView(session))
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploads": views})
}

func (s *Service) uploadStatus(w http.ResponseWriter, r *http.Request) {
	userID, composeID, ok := s.uploadCaller(w, r)
	if !ok {
		return
	}
	session, err := s.uploads.Get(r.Context(), userID, composeID, r.PathValue("uploadID"))
	if err != nil {
		writeUploadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, uploadView(session))
}

// appendChunk stores one chunk. Its length must be declared and at most
// chunkupload.MaxChunk, and Upload-Offset must name where the upload is.
func (s *Service) appendChunk(w http.ResponseWriter, r *http.Request) {
	userID, composeID, ok := s.uploadCaller(w, r)
	if !ok {
		return
	}
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "a chunk requires a declared length (Content-Length)")
		return
	}
	if r.ContentLength > chunkupload.MaxChunk {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("a chunk is at most %d MiB", chunkupload.MaxChunk>>20))
		return
	}
	offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, "a chunk requires an Upload-Offset header")
		return
	}
	body := http.MaxBytesReader(w, r.Body, chunkupload.MaxChunk)
	received, err := s.uploads.Append(r.Context(), userID, composeID, r.PathValue("uploadID"), offset, body, r.ContentLength)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(received, 10))
	w.WriteHeader(http.StatusNoContent)
}

// completeUpload replays the stored query through the direct write's checks,
// under the caller's current rights, and starts the background write.
func (s *Service) completeUpload(w http.ResponseWriter, r *http.Request) {
	userID, composeID, ok := s.uploadCaller(w, r)
	if !ok {
		return
	}
	id := r.PathValue("uploadID")
	session, err := s.uploads.Get(r.Context(), userID, composeID, id)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	projectID, err := parsePositiveID(r.PathValue("projectID"))
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	replay := r.Clone(r.Context())
	replay.URL.RawQuery = session.Target
	req, _, ok := s.contentOperation(w, replay, volumes.OpWrite)
	if !ok {
		return
	}
	req.DeclaredBytes = session.Size

	consume := func(ctx context.Context, staged io.Reader) error {
		releaseGate, err := s.locks.Acquire(ctx, projectID)
		if err != nil {
			return err
		}
		defer releaseGate()
		releaseVolumes, err := s.locks.AcquireVolumes(ctx, projectID)
		if err != nil {
			return err
		}
		defer releaseVolumes()
		// A Compose delete may have held the locks while this waited: its
		// cascade removed the upload, and `docker run -v` would recreate the
		// deleted volume just to write into it. Only a live upload is written.
		if _, err := s.uploads.Get(ctx, userID, composeID, id); err != nil {
			return &chunkupload.KindError{Kind: chunkupload.KindFailed, Err: fmt.Errorf("the upload was removed while it waited to be written: %w", err)}
		}
		return consumeError(s.runner.Run(ctx, req, staged, io.Discard))
	}
	session, err = s.uploads.Complete(r.Context(), userID, composeID, id, consume, completeWait)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	status := http.StatusOK
	if session.State == chunkupload.StateConsuming {
		status = http.StatusAccepted
	}
	writeJSON(w, status, uploadView(session))
}

func (s *Service) cancelUpload(w http.ResponseWriter, r *http.Request) {
	userID, composeID, ok := s.uploadCaller(w, r)
	if !ok {
		return
	}
	if err := s.uploads.Cancel(r.Context(), userID, composeID, r.PathValue("uploadID")); err != nil {
		writeUploadError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeUploadError answers the chunkupload errors every upload route shares.
func writeUploadError(w http.ResponseWriter, err error) {
	var offsetErr *chunkupload.OffsetError
	var stateErr *chunkupload.StateError
	switch {
	case errors.Is(err, chunkupload.ErrNotFound):
		writeError(w, http.StatusNotFound, "upload not found")
	case errors.As(err, &offsetErr):
		w.Header().Set("Upload-Offset", strconv.FormatInt(offsetErr.Received, 10))
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &stateErr), errors.Is(err, chunkupload.ErrBusy), errors.Is(err, chunkupload.ErrConsuming):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, chunkupload.ErrPastEnd), errors.Is(err, chunkupload.ErrShortBody):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("a chunk is at most %d MiB", chunkupload.MaxChunk>>20))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
