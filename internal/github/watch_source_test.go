package github

import (
	"context"
	"reflect"
	"testing"

	"github.com/maxbeizer/gh-babysit/internal/domain"
	"github.com/maxbeizer/gh-babysit/internal/process"
)

func TestFailedLogsBoundsSubprocessCaptureAndScopesRepository(t *testing.T) {
	var request process.Request
	runner := runnerFunc(func(_ context.Context, got process.Request) (process.Result, error) {
		request = got
		return process.Result{Stdout: []byte("logs")}, nil
	})
	source, err := NewWatchSource(runner, "/repo", domain.PullRequest{Host: "github.com", Repository: "acme/widgets", Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	runID := int64(42)
	logs, err := source.FailedLogs(context.Background(), Observation{WorkflowRunID: &runID}, 1024)
	if err != nil || logs != "logs" {
		t.Fatalf("logs=%q error=%v", logs, err)
	}
	want := []string{"run", "view", "42", "--repo", "acme/widgets", "--log-failed"}
	if request.Name != "gh" || !reflect.DeepEqual(request.Args, want) || request.StdoutLimit != 1024 || request.StderrLimit == 0 {
		t.Fatalf("request=%+v", request)
	}
}
