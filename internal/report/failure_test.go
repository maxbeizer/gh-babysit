package report

import (
	"bytes"
	"strings"
	"testing"

	gh "github.com/maxbeizer/gh-babysit/internal/github"
)

func TestFailureSummarizesActionsLogs(t *testing.T) {
	runID := int64(39332730470)
	logs := strings.Join([]string{
		"setup\tUNKNOWN STEP\t2025-01-02T03:04:05.000Z installing packages",
		"results\tcheck\t2025-01-02T03:04:06Z \x1b[31mFAILED github-reposd-multitenant https://github.com/acme/widgets/actions/runs/1/job/10\x1b[0m",
		"results\tcheck\t2025-01-02T03:04:07Z failed github-reposd https://github.com/acme/widgets/actions/runs/1/job/11",
		"results\tcheck\t2025-01-02T03:04:08Z Error: golden jobs failed",
		"results\tcheck\t2025-01-02T03:04:09Z Recommended: run bin/ci-summary",
	}, "\n")
	var out bytes.Buffer
	Failure(&out, FailureOptions{
		Check:      gh.Observation{Identity: gh.Identity{Context: "golden-jobs-results"}, URL: "https://checks.example/1", WorkflowRunID: &runID},
		Repository: "github.com/acme/widgets", Logs: logs,
	})
	got := out.String()
	for _, want := range []string{
		"Required check failed: golden-jobs-results",
		"- github-reposd-multitenant",
		"actions/runs/1/job/10",
		"- github-reposd",
		"Suggested next step:\n  Recommended: run bin/ci-summary",
		"Failure details:\n  Error: golden jobs failed",
		"gh run view 39332730470 --log-failed --repo github.com/acme/widgets",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"installing packages", "UNKNOWN STEP", "\x1b[31m", "Raw failed logs:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("output contains %q:\n%s", unwanted, got)
		}
	}
}

func TestFailureHandlesUnavailableAndExplicitRawLogs(t *testing.T) {
	for _, test := range []struct {
		name     string
		showLogs bool
		wantRaw  bool
	}{
		{name: "concise by default"},
		{name: "raw logs requested", showLogs: true, wantRaw: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			Failure(&out, FailureOptions{Check: gh.Observation{Identity: gh.Identity{Context: "external"}, URL: "https://checks.example/1"}, Logs: "unavailable: logs expired", ShowLogs: test.showLogs})
			got := out.String()
			if strings.Contains(got, "Raw failed logs:") != test.wantRaw {
				t.Fatalf("output=%q", got)
			}
			if strings.Count(got, "https://checks.example/1") != 1 {
				t.Fatalf("check link should appear once: output=%q", got)
			}
		})
	}
}
