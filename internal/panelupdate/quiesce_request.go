package panelupdate

import (
	"context"
	"errors"
	"io/fs"
	"time"
)

// Defaults of the quiesce request (spec "Apply" step 3).
const (
	// DefaultAnswerWindow is how long the panel has to answer a quiesce
	// request, and how long a heartbeat may stand still.
	DefaultAnswerWindow = 20 * time.Second
	// DefaultAckEvery is how often the Updater reads panel.json.
	DefaultAckEvery = 2 * time.Second
)

// quiesceOutcome is how the quiesce request ended.
type quiesceOutcome int

const (
	quiesceReady     quiesceOutcome = iota // the panel is idle and locked
	quiesceNoAnswer                        // the panel did not answer: it was stopped
	quiesceRefused                         // the panel refused: a Migration lock is held
	quiesceCancelled                       // a signal arrived before the backup
)

func (u *Updater) answerWindow() time.Duration {
	if u.AnswerWindow > 0 {
		return u.AnswerWindow
	}
	return DefaultAnswerWindow
}

func (u *Updater) ackEvery() time.Duration {
	if u.AckEvery > 0 {
		return u.AckEvery
	}
	return DefaultAckEvery
}

// signalled reports whether a cancel signal is waiting.
func (u *Updater) signalled() bool {
	if u.Signals == nil {
		return false
	}
	select {
	case <-u.Signals:
		return true
	default:
		return false
	}
}

// quiesce asks the panel to go quiet and waits for its answer in panel.json.
//
//   - A panel.json whose run is not this one is not an answer.
//   - "Answering" is a heartbeat that changes, never the panel's timestamp
//     against this Container's clock: the two Containers' clocks may differ.
//   - Time is counted in poll intervals, so it needs no clock either.
//   - Once the panel has answered, it is waited for with no time limit. A
//     heartbeat that stands still for the answer window while the panel
//     Container is not running means the panel is gone; while it is running,
//     it is only slow, and is waited for.
//
// Quiescing ends with the panel locked and idle, or with the panel stopped.
func (u *Updater) quiesce(ctx context.Context, dir string, st *RunStatus, in Install) (quiesceOutcome, string, error) {
	var (
		silent    time.Duration // time since the last sign of the panel
		answered  bool
		lastBeat  int64
		lastWait  string
		window    = u.answerWindow()
		everyPoll = u.ackEvery()
	)
	for {
		if u.signalled() {
			return quiesceCancelled, "cancelled", nil
		}
		ack, err := ReadAck(dir)
		fresh := err == nil && ack.Run == st.Run
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			u.logf("read panel.json: %v", err)
		}
		if fresh {
			if !answered || ack.Heartbeat != lastBeat {
				silent = 0
			}
			answered, lastBeat = true, ack.Heartbeat
			switch ack.State {
			case AckRefused:
				return quiesceRefused, ack.Reason, nil
			case AckReady:
				st.WaitingOn = ""
				return quiesceReady, "", nil
			}
			if ack.WaitingOn != lastWait {
				lastWait = ack.WaitingOn
				st.WaitingOn = ack.WaitingOn
				u.logf("waiting on %s", ack.WaitingOn)
				if err := WriteStatus(dir, *st, u.now()); err != nil {
					return 0, "", err
				}
			}
		}
		if silent >= window {
			if !answered {
				u.logf("the panel did not answer within %s", window)
				return quiesceNoAnswer, "", nil
			}
			running, err := u.panelRunning(ctx, in)
			if err != nil {
				u.logf("list the panel Containers: %v", err)
			} else if !running {
				u.logf("the panel stopped answering and is not running")
				return quiesceNoAnswer, "gone", nil
			}
		}
		if err := u.sleep(ctx, everyPoll); err != nil {
			return 0, "", err
		}
		silent += everyPoll
	}
}

// panelRunning reports whether a panel Container of the Install is running.
func (u *Updater) panelRunning(ctx context.Context, in Install) (bool, error) {
	panels, err := u.Host.Containers(ctx, in.Project, panelServiceName)
	if err != nil {
		return false, err
	}
	for _, c := range panels {
		if c.Running {
			return true, nil
		}
	}
	return false, nil
}
