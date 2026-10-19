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
	calls       int
}

func (f *fakeSource) HeadSHA(context.Context) (string, error) {
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

func TestRunReturnsActionableFailureWithBoundedLogsAndSuggestion(t *testing.T) {
	runID := int64(77)
	failed := obs("build", gh.StateFailure)
	failed.WorkflowRunID = &runID
	source := &fakeSource{snapshots: []gh.Snapshot{snapshot([]gh.Requirement{req("build")}, failed)}, logs: "0123456789"}
	result, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour, LogLimit: 6})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != Failed || result.Logs != "012\n[logs truncated]\n789" || result.RerunSuggestion != "gh run rerun 77 --failed" {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunReportsUnavailableLogsWithoutGuessingARerun(t *testing.T) {
	failed := obs("external", gh.StateFailure)
	source := &fakeSource{snapshots: []gh.Snapshot{snapshot([]gh.Requirement{req("external")}, failed)}, logErr: errors.New("logs unavailable")}
	result, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if result.Logs != "unavailable: logs unavailable" || result.RerunSuggestion != "" {
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

func TestRunFailsSafeOnHeadChangeAndTimeouts(t *testing.T) {
	t.Run("head change", func(t *testing.T) {
		source := &fakeSource{heads: []string{"sha", "other"}, snapshots: []gh.Snapshot{snapshot(nil)}}
		_, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Minute, Timeout: time.Hour})
		if err == nil || !strings.Contains(err.Error(), "head changed") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("appearance", func(t *testing.T) {
		source := &fakeSource{snapshots: []gh.Snapshot{snapshot([]gh.Requirement{req("build")}), snapshot([]gh.Requirement{req("build")})}}
		_, err := Run(context.Background(), source, &fakeClock{}, Options{RecordedSHA: "sha", PollInterval: time.Second, AppearanceTimeout: time.Second, Timeout: time.Hour})
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("error=%v", err)
		}
	})
}
