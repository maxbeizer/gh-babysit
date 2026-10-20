package watcher

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gh "github.com/maxbeizer/gh-babysit/internal/github"
)

type fakeSource struct {
	snapshots   []gh.Snapshot
	heads       []string
	logs        string
	snapshotErr error
	logErr      error
	localErrors []error
	block       <-chan struct{}
	calls       int
}

func (f *fakeSource) VerifyLocal(context.Context) error {
	if len(f.localErrors) == 0 {
		return nil
	}
	err := f.localErrors[0]
	f.localErrors = f.localErrors[1:]
	return err
}
func (f *fakeSource) HeadSHA(ctx context.Context) (string, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if len(f.heads) == 0 {
		return "sha", nil
	}
	v := f.heads[0]
	f.heads = f.heads[1:]
	return v, nil
}
func (f *fakeSource) Snapshot(context.Context) (gh.Snapshot, error) {
	if f.snapshotErr != nil {
		return gh.Snapshot{}, f.snapshotErr
	}
	if len(f.snapshots) == 0 {
		return gh.Snapshot{}, errors.New("no snapshot")
	}
	v := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	f.calls++
	return v, nil
}
func (f *fakeSource) FailedLogs(context.Context, gh.Observation, int) (string, error) {
	return f.logs, f.logErr
}

type fakeClock struct {
	now    time.Time
	sleeps int
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.sleeps++
	c.now = c.now.Add(d)
	return ctx.Err()
}

func req(context string) gh.Requirement {
	return gh.Requirement{Identity: gh.Identity{Context: context}}
}
func obs(context string, state gh.State) gh.Observation {
	return gh.Observation{Identity: gh.Identity{Context: context}, State: state, URL: "https://checks.example/1"}
}
func snapshot(required []gh.Requirement, observations ...gh.Observation) gh.Snapshot {
	return gh.Snapshot{SHA: "sha", RequirementsAuthoritative: true, Requirements: required, Observations: observations}
}

func TestRunWaitsForAppearanceAndTwoStableSuccessfulSnapshots(t *testing.T) {
	source := &fakeSource{snapshots: []gh.Snapshot{
		snapshot([]gh.Requirement{req("build")}),
		snapshot([]gh.Requirement{req("build")}, obs("build", gh.StatePending)),
		snapshot([]gh.Requirement{req("build")}, obs("build", gh.StateSuccess)),
		snapshot([]gh.Requirement{req("build")}, obs("build", gh.StateSuccess)),
	}}
	clock := &fakeClock{now: time.Unix(0, 0)}
	var out bytes.Buffer
	result, err := Run(context.Background(), source, clock, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour, Output: &out})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Success || source.calls != 4 || clock.sleeps != 3 {
		t.Fatalf("result=%+v calls=%d sleeps=%d", result, source.calls, clock.sleeps)
	}
	if !strings.Contains(out.String(), "awaiting-suite") || !strings.Contains(out.String(), "success (stable 2/2)") {
		t.Fatalf("output=%q", out.String())
	}
}

func TestRunRequiresTheSameSuccessfulSetTwice(t *testing.T) {
	source := &fakeSource{snapshots: []gh.Snapshot{
		snapshot([]gh.Requirement{req("build")}, obs("build", gh.StateSuccess)),
		snapshot([]gh.Requirement{req("lint")}, obs("lint", gh.StateSuccess)),
		snapshot([]gh.Requirement{req("lint")}, obs("lint", gh.StateSuccess)),
	}}
	result, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Success || source.calls != 3 {
		t.Fatalf("result=%+v calls=%d", result, source.calls)
	}
}

func TestEvaluateAppliesUnqualifiedRequirementsToAllMatchingObservations(t *testing.T) {
	appID := int64(15368)
	requirement := req("build")
	passingStatus := obs("build", gh.StateSuccess)
	failingCheck := obs("build", gh.StateFailure)
	failingCheck.AppID = &appID
	missing, pending, failed := evaluate(snapshot([]gh.Requirement{requirement}, passingStatus, failingCheck))
	if len(missing) != 0 || len(pending) != 0 || len(failed) != 1 || failed[0].AppID == nil {
		t.Fatalf("missing=%v pending=%v failed=%v", missing, pending, failed)
	}

	requirement.AppID = &appID
	otherApp := int64(99)
	passingStatus.AppID = &otherApp
	missing, _, failed = evaluate(snapshot([]gh.Requirement{requirement}, passingStatus))
	if len(missing) != 1 || len(failed) != 0 {
		t.Fatalf("app-qualified result missing=%v failed=%v", missing, failed)
	}
}

func TestRunReportsOptionalFailureWithoutGatingSuccess(t *testing.T) {
	s := snapshot([]gh.Requirement{req("build")}, obs("build", gh.StateSuccess), obs("lint", gh.StateFailure))
	source := &fakeSource{snapshots: []gh.Snapshot{s, s}}
	var out bytes.Buffer
	result, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour, Output: &out})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Success || !strings.Contains(out.String(), "optional lint: failure") {
		t.Fatalf("result=%+v output=%q", result, out.String())
	}
}

func TestRunReturnsActionableFailureWithBoundedLogs(t *testing.T) {
	runID := int64(77)
	failed := obs("build", gh.StateFailure)
	failed.WorkflowRunID = &runID
	source := &fakeSource{snapshots: []gh.Snapshot{snapshot([]gh.Requirement{req("build")}, failed)}, logs: "0123456789"}
	result, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour, LogLimit: 6})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Failed || result.Logs != "012\n[logs truncated]\n789" {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunSuggestsOnlyConservativelySafeReruns(t *testing.T) {
	for _, test := range []struct {
		name, conclusion, workflowConclusion, want string
	}{
		{name: "cancelled", conclusion: "cancelled", workflowConclusion: "cancelled", want: "gh run rerun 77 --failed --repo acme/widgets"},
		{name: "deterministic failure", conclusion: "failure", workflowConclusion: "failure"},
		{name: "timed out may be deterministic", conclusion: "timed_out", workflowConclusion: "timed_out"},
		{name: "cancelled job in failed workflow", conclusion: "cancelled", workflowConclusion: "failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runID := int64(77)
			failed := obs("build", gh.StateFailure)
			failed.Source, failed.WorkflowRunID, failed.Conclusion, failed.WorkflowConclusion = gh.SourceCheckRun, &runID, test.conclusion, test.workflowConclusion
			source := &fakeSource{snapshots: []gh.Snapshot{snapshot([]gh.Requirement{req("build")}, failed)}, logs: "logs"}
			result, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", Repository: "acme/widgets", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
			if err != nil || result.RerunSuggestion != test.want {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestRunRechecksRemoteHeadAfterFailedLogs(t *testing.T) {
	failed := obs("build", gh.StateFailure)
	source := &fakeSource{heads: []string{"sha", "sha", "other"}, snapshots: []gh.Snapshot{snapshot([]gh.Requirement{req("build")}, failed)}, logs: "logs"}
	_, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "while reading failed logs") {
		t.Fatalf("error=%v", err)
	}
}

func TestRunReportsUnavailableLogsWithoutGuessingARerun(t *testing.T) {
	failed := obs("external", gh.StateFailure)
	source := &fakeSource{snapshots: []gh.Snapshot{snapshot([]gh.Requirement{req("external")}, failed)}, logErr: errors.New("logs unavailable")}
	result, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if result.Logs != "unavailable: logs unavailable" {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunPropagatesProviderAndCancellationFailures(t *testing.T) {
	_, err := Run(context.Background(), &fakeSource{snapshotErr: errors.New("rate limited")}, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("provider error=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := &fakeSource{snapshots: []gh.Snapshot{snapshot([]gh.Requirement{req("build")}, obs("build", gh.StatePending))}}
	_, err = Run(ctx, source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
}

type rateLimitError struct{ wait time.Duration }

func (e rateLimitError) Error() string                      { return "rate limited" }
func (e rateLimitError) RetryAfter(time.Time) time.Duration { return e.wait }

type sequencedSource struct {
	fakeSource
	errors []error
}

func (s *sequencedSource) Snapshot(ctx context.Context) (gh.Snapshot, error) {
	if len(s.errors) > 0 {
		err := s.errors[0]
		s.errors = s.errors[1:]
		if err != nil {
			return gh.Snapshot{}, err
		}
	}
	return s.fakeSource.Snapshot(ctx)
}

func TestRunRetriesRateLimitsWithinOverallDeadline(t *testing.T) {
	success := snapshot(nil)
	source := &sequencedSource{fakeSource: fakeSource{snapshots: []gh.Snapshot{success, success}}, errors: []error{rateLimitError{wait: 2 * time.Second}}}
	clock := &fakeClock{}
	result, err := Run(context.Background(), source, clock, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Minute})
	if err != nil || result.State != Success || clock.now.Sub(time.Time{}) != 3*time.Second {
		t.Fatalf("result=%+v error=%v now=%s", result, err, clock.now)
	}

	source = &sequencedSource{errors: []error{rateLimitError{wait: 2 * time.Minute}}}
	_, err = Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Minute})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error=%v", err)
	}
}

func TestOverallTimeoutCancelsBlockedOperations(t *testing.T) {
	for _, stage := range []string{"head", "snapshot", "logs"} {
		t.Run(stage, func(t *testing.T) {
			blocked := make(chan struct{})
			source := &fakeSource{block: blocked}
			if stage != "head" {
				source.block = nil
			}
			if stage == "snapshot" {
				source.snapshotErr = nil
				source.snapshots = nil
			}
			if stage == "logs" {
				failed := obs("build", gh.StateFailure)
				source.snapshots = []gh.Snapshot{snapshot([]gh.Requirement{req("build")}, failed)}
			}
			ctxSource := Source(source)
			if stage != "head" {
				ctxSource = blockingSource{Source: source, stage: stage, blocked: blocked}
			}
			_, err := Run(context.Background(), ctxSource, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: 20 * time.Millisecond})
			if !errors.Is(err, ErrTimeout) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

type blockingSource struct {
	Source
	stage   string
	blocked <-chan struct{}
}

func (s blockingSource) Snapshot(ctx context.Context) (gh.Snapshot, error) {
	if s.stage != "snapshot" {
		return s.Source.Snapshot(ctx)
	}
	select {
	case <-s.blocked:
		return gh.Snapshot{}, nil
	case <-ctx.Done():
		return gh.Snapshot{}, ctx.Err()
	}
}
func (s blockingSource) FailedLogs(ctx context.Context, o gh.Observation, n int) (string, error) {
	if s.stage != "logs" {
		return s.Source.FailedLogs(ctx, o, n)
	}
	select {
	case <-s.blocked:
		return "", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestRunStopsWhenLocalBaselineChangesAtPollingBoundary(t *testing.T) {
	source := &fakeSource{localErrors: []error{nil, errors.New("local index changed")}, snapshots: []gh.Snapshot{snapshot(nil)}}
	_, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "local index changed") {
		t.Fatalf("error=%v", err)
	}
}

func TestRunFailsSafeOnHeadChangeAndTimeouts(t *testing.T) {
	t.Run("head change", func(t *testing.T) {
		source := &fakeSource{heads: []string{"sha", "other"}, snapshots: []gh.Snapshot{snapshot(nil)}}
		_, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
		if err == nil || !strings.Contains(err.Error(), "head changed") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("appearance", func(t *testing.T) {
		source := &fakeSource{snapshots: []gh.Snapshot{
			snapshot([]gh.Requirement{req("build")}),
			snapshot([]gh.Requirement{req("build")}, obs("build", gh.StateSuccess)),
		}}
		clock := &fakeClock{}
		_, err := Run(context.Background(), source, clock, Options{RecordedSHA: "sha", PollInterval: 15 * time.Second, AppearanceTimeout: time.Second, Timeout: time.Hour})
		if !errors.Is(err, ErrTimeout) || source.calls != 1 || clock.now.Sub(time.Time{}) != time.Second {
			t.Fatalf("error=%v calls=%d now=%s", err, source.calls, clock.now)
		}
	})
}
