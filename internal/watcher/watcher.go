// Package watcher evaluates authoritative required-check snapshots without mutating GitHub or the checkout.
package watcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/maxbeizer/gh-babysit/internal/clock"
	gh "github.com/maxbeizer/gh-babysit/internal/github"
)

type Source interface {
	HeadSHA(context.Context) (string, error)
	Snapshot(context.Context) (gh.Snapshot, error)
	FailedLogs(context.Context, gh.Observation, int) (string, error)
}

type State string

const (
	Success State = "success"
	Failed  State = "failed"
)

var ErrTimeout = errors.New("watch timeout")

type Options struct {
	RecordedSHA                              string
	PollInterval, AppearanceTimeout, Timeout time.Duration
	LogLimit                                 int
	Output                                   io.Writer
}

type Result struct {
	State                 State
	Snapshot              gh.Snapshot
	Failed                gh.Observation
	Logs, RerunSuggestion string
}

func Run(ctx context.Context, source Source, timer clock.Clock, options Options) (Result, error) {
	if source == nil || timer == nil || options.RecordedSHA == "" {
		return Result{}, errors.New("watcher requires a source, clock, and recorded SHA")
	}
	if options.LogLimit <= 0 {
		options.LogLimit = 64 << 10
	}
	started, appeared, stable := timer.Now(), false, 0
	for {
		if timer.Now().Sub(started) >= options.Timeout {
			return Result{}, fmt.Errorf("%w: overall timeout after %s", ErrTimeout, options.Timeout)
		}
		before, err := source.HeadSHA(ctx)
		if err != nil {
			return Result{}, fmt.Errorf("read PR head before snapshot: %w", err)
		}
		if before != options.RecordedSHA {
			return Result{}, fmt.Errorf("safety stop: PR head changed from %s to %s", options.RecordedSHA, before)
		}
		snapshot, err := source.Snapshot(ctx)
		if err != nil {
			return Result{}, fmt.Errorf("discover required checks: %w", err)
		}
		after, err := source.HeadSHA(ctx)
		if err != nil {
			return Result{}, fmt.Errorf("read PR head after snapshot: %w", err)
		}
		if after != before || snapshot.SHA != options.RecordedSHA {
			return Result{}, fmt.Errorf("safety stop: PR head changed while reading checks")
		}
		if !snapshot.RequirementsAuthoritative {
			return Result{}, errors.New("safety stop: required-check configuration is not authoritative")
		}

		missing, pending, failed := evaluate(snapshot)
		if len(failed) > 0 {
			observation := failed[0]
			logs, logErr := source.FailedLogs(ctx, observation, options.LogLimit)
			result := Result{State: Failed, Snapshot: snapshot, Failed: observation}
			if logErr != nil {
				result.Logs = "unavailable: " + logErr.Error()
			} else {
				result.Logs = bound(logs, options.LogLimit)
			}
			if observation.WorkflowRunID != nil {
				result.RerunSuggestion = fmt.Sprintf("gh run rerun %d --failed", *observation.WorkflowRunID)
			}
			print(options.Output, "failed %s: %s\n", observation.Context, observation.URL)
			return result, nil
		}
		if len(snapshot.Requirements) == 0 || len(missing) == 0 {
			appeared = true
		}
		if appeared && len(missing) == 0 && len(pending) == 0 {
			stable++
			print(options.Output, "success (stable %d/2)\n", stable)
			if stable == 2 {
				reportOptional(options.Output, snapshot)
				return Result{State: Success, Snapshot: snapshot}, nil
			}
		} else {
			stable = 0
			state := "waiting-required"
			if !appeared {
				state = "awaiting-suite"
			}
			print(options.Output, "%s: missing=%d pending=%d\n", state, len(missing), len(pending))
		}
		if !appeared && timer.Now().Sub(started) >= options.AppearanceTimeout {
			return Result{}, fmt.Errorf("%w: required suite did not appear after %s", ErrTimeout, options.AppearanceTimeout)
		}
		if err := timer.Sleep(ctx, options.PollInterval); err != nil {
			return Result{}, err
		}
	}
}

func evaluate(snapshot gh.Snapshot) (missing []gh.Requirement, pending, failed []gh.Observation) {
	for _, requirement := range snapshot.Requirements {
		var found *gh.Observation
		for i := range snapshot.Observations {
			if sameIdentity(requirement.Identity, snapshot.Observations[i].Identity) {
				found = &snapshot.Observations[i]
				break
			}
		}
		if found == nil {
			missing = append(missing, requirement)
			continue
		}
		switch found.State {
		case gh.StateSuccess, gh.StateNeutral, gh.StateSkipped:
		default:
			if found.State == gh.StatePending {
				pending = append(pending, *found)
			} else {
				failed = append(failed, *found)
			}
		}
	}
	return
}
func sameIdentity(a, b gh.Identity) bool {
	if !strings.EqualFold(a.Context, b.Context) || (a.AppID == nil) != (b.AppID == nil) {
		return false
	}
	return a.AppID == nil || *a.AppID == *b.AppID
}
func reportOptional(out io.Writer, snapshot gh.Snapshot) {
	for _, observation := range snapshot.Observations {
		required := false
		for _, requirement := range snapshot.Requirements {
			if sameIdentity(requirement.Identity, observation.Identity) {
				required = true
				break
			}
		}
		if !required {
			print(out, "optional %s: %s\n", observation.Context, observation.State)
		}
	}
}
func bound(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	half := limit / 2
	return value[:half] + "\n[logs truncated]\n" + value[len(value)-(limit-half):]
}
func print(out io.Writer, format string, args ...any) {
	if out != nil {
		fmt.Fprintf(out, format, args...)
	}
}
