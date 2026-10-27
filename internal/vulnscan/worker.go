package vulnscan

import (
	"context"
	"errors"
	"strings"
	"time"
)

// idleWait bounds how long an idle worker sleeps without a wake-up.
const idleWait = 30 * time.Second

// Run clears what a restart left, makes sure the Scanner is ready when
// scanning is on, and then works the queue one task at a time until ctx is
// done. Before each task it waits while the Deployment queue is busy.
func (s *Service) Run(ctx context.Context) {
	if err := s.d.Scanner.Cleanup(ctx); err != nil {
		logf("clean up after restart: %v", err)
	}
	if _, err := s.d.Store.Unstart(ctx); err != nil {
		logf("requeue interrupted scans: %v", err)
	}
	s.startup(ctx)
	for ctx.Err() == nil {
		task, ok, err := s.d.Store.Next(ctx)
		if err != nil {
			logf("read the queue: %v", err)
			sleep(ctx, 5*time.Second)
			continue
		}
		if !ok {
			select {
			case <-ctx.Done():
			case <-s.wake:
			case <-time.After(idleWait):
			}
			continue
		}
		for s.d.Gate.Busy() && ctx.Err() == nil {
			sleep(ctx, s.d.Tick)
		}
		if ctx.Err() != nil {
			return
		}
		if err := s.d.Store.Start(ctx, task.ID, s.d.Now()); err != nil {
			logf("start task %d: %v", task.ID, err)
			sleep(ctx, 5*time.Second)
			continue
		}
		s.process(ctx, task)
		if err := s.d.Store.Done(context.WithoutCancel(ctx), task.ID); err != nil {
			logf("finish task %d: %v", task.ID, err)
			sleep(ctx, 5*time.Second)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// startup restores what a prune, a Migration or a release that bumped the
// default image took away: the Scanner image, and a DB to scan with.
func (s *Service) startup(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.d.Store.Settings(ctx)
	if err != nil || !st.Enabled {
		return
	}
	ref := st.EffectiveImage()
	if err := s.d.Scanner.EnsureImage(ctx, ref); err != nil {
		logf("prepare the scanner image %s: %v", ref, err)
		return
	}
	if ref != st.ActiveImage {
		if err := s.d.Store.SetActiveImage(ctx, ref); err != nil {
			logf("record the scanner image: %v", err)
			return
		}
		if st.ActiveImage != "" {
			if err := s.d.Scanner.RemoveImage(ctx, st.ActiveImage); err != nil {
				logf("remove the old scanner image %s: %v", st.ActiveImage, err)
			}
		}
		s.enqueueScanner(ctx, ref, ReasonSetup)
	}
	if st.DBVersion == "" {
		info, err := s.d.Scanner.DBInfo(ctx, ref)
		switch {
		case errors.Is(err, ErrNoDB):
			s.d.Store.Enqueue(ctx, KindDBUpdate, "", ReasonSetup, s.d.Now())
		case err != nil:
			logf("read the vulnerability DB: %v", err)
		default:
			s.d.Store.SetDB(ctx, info.Version, s.d.Now(), "")
		}
	}
}

func (s *Service) process(ctx context.Context, task Task) {
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		logf("read settings: %v", err)
		return
	}
	if !st.Enabled {
		return
	}
	ref := st.EffectiveImage()
	switch task.Kind {
	case KindDBUpdate:
		s.updateDB(ctx, ref)
	case KindImage:
		s.scanImage(ctx, st, ref, task)
	}
}

func (s *Service) updateDB(ctx context.Context, ref string) {
	if err := s.d.Scanner.EnsureImage(ctx, ref); err != nil {
		s.d.Store.SetDB(ctx, "", s.d.Now(), err.Error())
		return
	}
	info, err := s.d.Scanner.UpdateDB(ctx, ref)
	if err != nil {
		s.d.Store.SetDB(ctx, "", s.d.Now(), err.Error())
		return
	}
	if err := s.d.Store.SetDB(ctx, info.Version, s.d.Now(), ""); err != nil {
		logf("record the DB update: %v", err)
	}
}

func (s *Service) scanImage(ctx context.Context, st Settings, ref string, task Task) {
	bg := context.WithoutCancel(ctx)
	imageRef := s.displayRef(ctx, task.ImageID)
	dbVersion := st.DBVersion
	if dbVersion == "" {
		info, err := s.d.Scanner.DBInfo(ctx, ref)
		if err != nil {
			s.d.Store.RecordFailure(bg, task.ImageID, imageRef, err.Error(), s.d.Now())
			return
		}
		dbVersion = info.Version
		s.d.Store.SetDB(ctx, dbVersion, s.d.Now(), "")
	}
	if task.Reason != ReasonManual {
		version, errText, found, err := s.d.Store.ScanState(ctx, task.ImageID)
		if err == nil && found && errText == "" && version == dbVersion {
			return
		}
	}
	report, err := s.d.Scanner.Scan(ctx, ref, task.ImageID)
	if err != nil {
		s.d.Store.RecordFailure(bg, task.ImageID, imageRef, err.Error(), s.d.Now())
		return
	}
	if err := s.d.Store.RecordScan(bg, task.ImageID, imageRef, report, dbVersion, s.d.Now()); err != nil {
		logf("record the scan of %s: %v", task.ImageID, err)
	}
}

// displayRef names an image by its tags, or by a short ID when it has none.
func (s *Service) displayRef(ctx context.Context, imageID string) string {
	if used, err := s.UsedImages(ctx); err == nil {
		for _, image := range used {
			if image.ImageID == imageID && len(image.Refs) > 0 {
				return strings.Join(image.Refs, ", ")
			}
		}
	}
	short := strings.TrimPrefix(imageID, "sha256:")
	return short[:min(12, len(short))]
}
