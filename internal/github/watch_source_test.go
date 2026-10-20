package github

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/maxbeizer/gh-babysit/internal/domain"
	"github.com/maxbeizer/gh-babysit/internal/process"
)

func TestVerifyLocalComparesHeadWorktreeAndIndexToBaseline(t *testing.T) {
	outputs := [][]byte{[]byte("sha\n"), nil, []byte("index\x00"), []byte("sha\n"), []byte("?? changed\x00")}
	runner := runnerFunc(func(_ context.Context, got process.Request) (process.Result, error) {
		if got.Name != "git" || got.Dir != "/repo" {
			t.Fatalf("request=%+v", got)
		}
		out := outputs[0]
		outputs = outputs[1:]
		return process.Result{Stdout: out}, nil
	})
	source, err := NewWatchSource(runner, "/repo", domain.PullRequest{Repository: "acme/widgets"}, "sha", nil, []byte("index\x00"))
	if err != nil {
		t.Fatal(err)
	}
	if err := source.VerifyLocal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := source.VerifyLocal(context.Background()); err == nil {
		t.Fatal("expected changed worktree safety stop")
	}
}

func TestHeadSHAUsesEnterpriseHostAndRejectsRetargeting(t *testing.T) {
	responses := [][]byte{
		[]byte(`{"headRefOid":"sha","baseRefName":"main"}`),
		[]byte(`{"headRefOid":"sha","baseRefName":"release"}`),
	}
	var requests []process.Request
	runner := runnerFunc(func(_ context.Context, got process.Request) (process.Result, error) {
		requests = append(requests, got)
		response := responses[0]
		responses = responses[1:]
		return process.Result{Stdout: response}, nil
	})
	source, err := NewWatchSource(runner, "/repo", domain.PullRequest{Host: "github.example.com", Repository: "acme/widgets", Number: 7, BaseBranch: "main"}, "sha", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if head, err := source.HeadSHA(context.Background()); err != nil || head != "sha" {
		t.Fatalf("head=%q error=%v", head, err)
	}
	want := []string{"pr", "view", "7", "--repo", "github.example.com/acme/widgets", "--json", "headRefOid,baseRefName"}
	if !reflect.DeepEqual(requests[0].Args, want) {
		t.Fatalf("args=%v want=%v", requests[0].Args, want)
	}
	if _, err := source.HeadSHA(context.Background()); err == nil || !strings.Contains(err.Error(), "base changed") {
		t.Fatalf("retarget error=%v", err)
	}
}

func TestFailedLogsBoundsSubprocessCaptureAndScopesRepository(t *testing.T) {
	var request process.Request
	runner := runnerFunc(func(_ context.Context, got process.Request) (process.Result, error) {
		request = got
		return process.Result{Stdout: []byte("abcxyz"), StdoutTruncated: true}, nil
	})
	source, err := NewWatchSource(runner, "/repo", domain.PullRequest{Host: "github.com", Repository: "acme/widgets", Number: 1}, "sha", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runID := int64(42)
	logs, err := source.FailedLogs(context.Background(), Observation{WorkflowRunID: &runID}, 1024)
	if err != nil || logs != "abc\n[logs truncated]\nxyz" {
		t.Fatalf("logs=%q error=%v", logs, err)
	}
	want := []string{"run", "view", "42", "--repo", "github.com/acme/widgets", "--log-failed"}
	if request.Name != "gh" || !reflect.DeepEqual(request.Args, want) || request.StdoutLimit != 1024 || request.StderrLimit == 0 {
		t.Fatalf("request=%+v", request)
	}
}
