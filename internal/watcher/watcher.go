// Package watcher evaluates authoritative required-check snapshots without mutating GitHub or the checkout.
package watcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/maxbeizer/gh-babysit/internal/clock"
	gh "github.com/maxbeizer/gh-babysit/internal/github"
)

type Source interface {
	VerifyLocal(context.Context) error
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
	RecordedSHA, Repository                  string
	PollInterval, AppearanceTimeout, Timeout time.Duration
	LogLimit                                 int
	Output                                   io.Writer
}

type Result struct {
	State           State
	Snapshot        gh.Snapshot
	Failed          gh.Observation
	Logs            string
	RerunSuggestion string
}

func Run(ctx context.Context, source Source, timer clock.Clock, options Options) (Result, error) {
	if source == nil || timer == nil || options.RecordedSHA == "" {
		return Result{}, errors.New("watcher requires a source, clock, and recorded SHA")
	}
	if options.LogLimit <= 0 {
		options.LogLimit = 64 << 10
	}
	watchCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	started, appeared, stable := timer.Now(), false, 0
	deadline := started.Add(options.Timeout)
	appearanceDeadline := started.Add(options.AppearanceTimeout)
	var previousSuccess string
	for {
		operationDeadline := deadline
		if !appeared && appearanceDeadline.Before(operationDeadline) {
			operationDeadline = appearanceDeadline
		}
		if !timer.Now().Before(operationDeadline) {
			return Result{}, deadlineError(appeared, options)
		}
		if err := callVoid(watchCtx, timer, operationDeadline, source.VerifyLocal); err != nil {
			return Result{}, operationDeadlineError("verify local baseline", err, appeared, options)
		}
		before, err := call(watchCtx, timer, operationDeadline, source.HeadSHA)
		if err != nil {
			return Result{}, operationDeadlineError("read PR head before snapshot", err, appeared, options)
		}
		if before != options.RecordedSHA {
			return Result{}, fmt.Errorf("safety stop: PR head changed from %s to %s", options.RecordedSHA, before)
		}
		snapshot, err := call(watchCtx, timer, operationDeadline, source.Snapshot)
		if err != nil {
			return Result{}, operationDeadlineError("discover required checks", err, appeared, options)
		}
		after, err := call(watchCtx, timer, operationDeadline, source.HeadSHA)
		if err != nil {
			return Result{}, operationDeadlineError("read PR head after snapshot", err, appeared, options)
		}
		if after != before || snapshot.SHA != options.RecordedSHA {
			return Result{}, fmt.Errorf("safety stop: PR head changed while reading checks")
		}
		if err := callVoid(watchCtx, timer, operationDeadline, source.VerifyLocal); err != nil {
			return Result{}, operationDeadlineError("verify local baseline", err, appeared, options)
		}
		if !snapshot.RequirementsAuthoritative {
			return Result{}, errors.New("safety stop: required-check configuration is not authoritative")
		}

		missing, pending, failed := evaluate(snapshot)
		if !appeared && !timer.Now().Before(appearanceDeadline) {
			return Result{}, deadlineError(false, options)
		}
		if len(failed) > 0 {
			observation := failed[0]
			logs, logErr := call(watchCtx, timer, deadline, func(ctx context.Context) (string, error) {
				return source.FailedLogs(ctx, observation, options.LogLimit)
			})
			result := Result{State: Failed, Snapshot: snapshot, Failed: observation}
			if logErr != nil {
				if errors.Is(logErr, context.Canceled) || gh.IsAuthenticationError(logErr) || errors.Is(logErr, exec.ErrNotFound) {
					return Result{}, logErr
				}
				if errors.Is(logErr, context.DeadlineExceeded) || errors.Is(logErr, ErrTimeout) {
					return Result{}, timeoutError(options.Timeout)
				}
				result.Logs = "unavailable: " + logErr.Error()
			} else {
				result.Logs = bound(logs, options.LogLimit)
			}
			if err := source.VerifyLocal(watchCtx); err != nil {
				return Result{}, operationError("verify local baseline", err, options.Timeout)
			}
			currentHead, err := call(watchCtx, timer, deadline, source.HeadSHA)
			if err != nil {
				return Result{}, operationError("read PR head after failed logs", err, options.Timeout)
			}
			if currentHead != options.RecordedSHA {
				return Result{}, fmt.Errorf("safety stop: PR head changed from %s to %s while reading failed logs", options.RecordedSHA, currentHead)
			}
			if observation.Source == gh.SourceCheckRun && observation.WorkflowRunID != nil && observation.Conclusion == "cancelled" && options.Repository != "" {
				result.RerunSuggestion = fmt.Sprintf("gh run rerun %d --failed --repo %s", *observation.WorkflowRunID, options.Repository)
			}
			print(options.Output, "failed %s: %s\n", observation.Context, observation.URL)
			return result, nil
		}
		if len(snapshot.Requirements) == 0 || len(missing) == 0 {
			appeared = true
		}
		if appeared && len(missing) == 0 && len(pending) == 0 {
			currentSuccess := successfulSet(snapshot)
			if currentSuccess == previousSuccess {
				stable++
			} else {
				stable, previousSuccess = 1, currentSuccess
			}
			print(options.Output, "success (stable %d/2)\n", stable)
			if stable == 2 {
				reportOptional(options.Output, snapshot)
				return Result{State: Success, Snapshot: snapshot}, nil
			}
		} else {
			stable, previousSuccess = 0, ""
			state := "waiting-required"
			if !appeared {
				state = "awaiting-suite"
			}
			print(options.Output, "%s: missing=%d pending=%d\n", state, len(missing), len(pending))
		}
		operationDeadline = deadline
		if !appeared && appearanceDeadline.Before(operationDeadline) {
			operationDeadline = appearanceDeadline
		}
		remaining := operationDeadline.Sub(timer.Now())
		if remaining <= 0 {
			return Result{}, deadlineError(appeared, options)
		}
		sleep := min(options.PollInterval, remaining)
		if err := timer.Sleep(watchCtx, sleep); err != nil {
			return Result{}, operationDeadlineError("wait for next poll", err, appeared, options)
		}
	}
}

type retryableRateLimit interface {
	RetryAfter(time.Time) time.Duration
}

func call[T any](ctx context.Context, timer clock.Clock, deadline time.Time, operation func(context.Context) (T, error)) (T, error) {
	for {
		remaining := deadline.Sub(timer.Now())
		if remaining <= 0 {
			var zero T
			return zero, ErrTimeout
		}
		operationCtx, cancel := context.WithTimeout(ctx, remaining)
		value, err := operation(operationCtx)
		cancel()
		if err == nil && !timer.Now().Before(deadline) {
			return value, ErrTimeout
		}
		if err == nil {
			return value, nil
		}
		if ctx.Err() != nil {
			return value, ctx.Err()
		}
		var limited retryableRateLimit
		if !errors.As(err, &limited) {
			return value, err
		}
		wait := limited.RetryAfter(timer.Now())
		if wait <= 0 {
			wait = time.Second
		}
		if !timer.Now().Add(wait).Before(deadline) {
			return value, ErrTimeout
		}
		if err := timer.Sleep(ctx, wait); err != nil {
			return value, err
		}
	}
}

func callVoid(ctx context.Context, timer clock.Clock, deadline time.Time, operation func(context.Context) error) error {
	_, err := call(ctx, timer, deadline, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, operation(ctx)
	})
	return err
}

func deadlineError(appeared bool, options Options) error {
	if !appeared {
		return fmt.Errorf("%w: required suite did not appear after %s", ErrTimeout, options.AppearanceTimeout)
	}
	return timeoutError(options.Timeout)
}

func operationDeadlineError(operation string, err error, appeared bool, options Options) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout) {
		return deadlineError(appeared, options)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func operationError(operation string, err error, timeout time.Duration) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout) {
		return timeoutError(timeout)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func timeoutError(timeout time.Duration) error {
	return fmt.Errorf("%w: overall timeout after %s", ErrTimeout, timeout)
}

func evaluate(snapshot gh.Snapshot) (missing []gh.Requirement, pending, failed []gh.Observation) {
	for _, requirement := range snapshot.Requirements {
		var applicable []gh.Observation
		for _, observation := range snapshot.Observations {
			if satisfies(requirement.Identity, observation.Identity) {
				applicable = append(applicable, observation)
			}
		}
		if len(applicable) == 0 {
			missing = append(missing, requirement)
			continue
		}
		var requirementFailed bool
		for _, observation := range applicable {
			if observation.State != gh.StateSuccess && observation.State != gh.StateNeutral && observation.State != gh.StateSkipped && observation.State != gh.StatePending {
				failed = append(failed, observation)
				requirementFailed = true
			}
		}
		if requirementFailed {
			continue
		}
		for _, observation := range applicable {
			if observation.State == gh.StatePending {
				pending = append(pending, observation)
			}
		}
	}
	return
}
func satisfies(requirement, observation gh.Identity) bool {
	if !strings.EqualFold(requirement.Context, observation.Context) {
		return false
	}
	return requirement.AppID == nil || (observation.AppID != nil && *requirement.AppID == *observation.AppID)
}
func successfulSet(snapshot gh.Snapshot) string {
	identities := make([]string, 0, len(snapshot.Requirements))
	for _, requirement := range snapshot.Requirements {
		app := "unqualified"
		if requirement.AppID != nil {
			app = fmt.Sprintf("app:%d", *requirement.AppID)
		}
		identities = append(identities, strings.ToLower(requirement.Context)+"\x00"+app)
	}
	slices.Sort(identities)
	return strings.Join(identities, "\x01")
}

func reportOptional(out io.Writer, snapshot gh.Snapshot) {
	for _, observation := range snapshot.Observations {
		required := false
		for _, requirement := range snapshot.Requirements {
			if satisfies(requirement.Identity, observation.Identity) {
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
