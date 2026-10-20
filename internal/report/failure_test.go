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

func TestFailureSummarizesRealAggregateLogShape(t *testing.T) {
	runID := int64(39332730470)
	tests := []struct {
		name     string
		ansi     string
		unwanted string
	}{
		{name: "caret-rendered ANSI", ansi: `^[[36;1m`, unwanted: `^[[`},
		{name: "escape-byte ANSI", ansi: "\x1b[36;1m", unwanted: "\x1b["},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := strings.ReplaceAll(strings.Join([]string{
				"golden-jobs-results\tUNKNOWN STEP\t2026-02-02T18:05:01.000Z ##[group]Run echo \"time_in_ms=$(date +%s%3N)\" >> $GITHUB_OUTPUT",
				"golden-jobs-results\tUNKNOWN STEP\t2026-02-02T18:05:02.000Z ##[group]Run github/internal-actions@main",
				"golden-jobs-results\tUNKNOWN STEP\t2026-02-02T18:05:03.000Z ##[group]Run RUNNER_WORK_DIR=\"...\"",
				"golden-jobs-results\tUNKNOWN STEP\t2026-02-02T18:05:04.000Z " + tt.ansi + `if [[ -n "$failed_jobs" ]]; then` + "\x1b[0m",
				`golden-jobs-results\tUNKNOWN STEP\t2026-02-02T18:05:05.000Z   failed_jobs="$(jq -r '.jobs[] | select(.conclusion == "failure")')"`,
				`golden-jobs-results\tUNKNOWN STEP\t2026-02-02T18:05:06.000Z ##[error]golden-jobs (...) / github-reposd-multitenant failed child (https://github.com/github/github/actions/runs/39332730470/job/118334691274)`,
				`golden-jobs-results\tUNKNOWN STEP\t2026-02-02T18:05:07.000Z ##[error]golden-jobs (...) / github-reposd failed child (https://github.com/github/github/actions/runs/39332730470/job/118334691128)`,
				`golden-jobs-results\tUNKNOWN STEP\t2026-02-02T18:05:08.000Z {"name":"github-reposd","conclusion":"failure"},`,
				`golden-jobs-results\tUNKNOWN STEP\t2026-02-02T18:05:09.000Z Recommendation: run bin/ci-summary for failure details`,
			}, "\n"), `\t`, "\t")
			var out bytes.Buffer
			Failure(&out, FailureOptions{
				Check: gh.Observation{
					Identity:      gh.Identity{Context: "golden-jobs-results"},
					URL:           "https://github.com/github/github/actions/runs/39332730470/job/118343050137",
					WorkflowRunID: &runID,
				},
				Repository: "github/github",
				Logs:       logs,
			})
			got := out.String()
			for _, want := range []string{
				"Required check failed: golden-jobs-results",
				"- github-reposd-multitenant\n    https://github.com/github/github/actions/runs/39332730470/job/118334691274",
				"- github-reposd\n    https://github.com/github/github/actions/runs/39332730470/job/118334691128",
				"Suggested next step:\n  Recommendation: run bin/ci-summary for failure details",
				"gh run view 39332730470 --log-failed --repo github/github",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q:\n%s", want, got)
				}
			}
			for _, unwanted := range []string{
				"##[group]", "##[error]", "##[]", "UNKNOWN STEP", tt.unwanted,
				"time_in_ms", "github/internal-actions@main", "RUNNER_WORK_DIR", "failed_jobs", `"conclusion":"failure"`,
			} {
				if strings.Contains(got, unwanted) {
					t.Errorf("output contains %q:\n%s", unwanted, got)
				}
			}
			for _, childURL := range []string{
				"https://github.com/github/github/actions/runs/39332730470/job/118334691274",
				"https://github.com/github/github/actions/runs/39332730470/job/118334691128",
			} {
				if strings.Count(got, childURL) != 1 {
					t.Errorf("child URL should appear once: %s\n%s", childURL, got)
				}
			}
		})
	}
}

func TestFailureExtractsOrdinaryAndAggregateFailures(t *testing.T) {
	tests := []struct {
		name     string
		logs     string
		want     []string
		unwanted []string
	}{
		{
			name:     "ordinary failure with limited context",
			logs:     "runner setup\ncompiling package\nError: assertion failed\nexpected true, got false\nuploading artifacts",
			want:     []string{"compiling package", "Error: assertion failed", "expected true, got false"},
			unwanted: []string{"runner setup", "uploading artifacts"},
		},
		{
			name:     "aggregate failure with cleaned child link",
			logs:     "results\tUNKNOWN STEP\t2025-01-02T03:04:06Z \x1b[31mFAILED child-job https://github.com/acme/widgets/actions/runs/1/job/10\x1b[0m",
			want:     []string{"Failed jobs:", "- child-job", "actions/runs/1/job/10"},
			unwanted: []string{"UNKNOWN STEP", "2025-01-02", "\x1b[31m"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			Failure(&out, FailureOptions{Check: gh.Observation{Identity: gh.Identity{Context: "build"}, URL: "https://checks.example/1"}, Logs: tt.logs})
			got := out.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q:\n%s", want, got)
				}
			}
			for _, unwanted := range tt.unwanted {
				if strings.Contains(got, unwanted) {
					t.Errorf("output contains %q:\n%s", unwanted, got)
				}
			}
		})
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
