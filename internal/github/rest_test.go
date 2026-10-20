package github

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maxbeizer/gh-babysit/internal/process"
)

type fixtureAPI struct {
	responses map[string][]json.RawMessage
	errors    map[string]error
}

func (f fixtureAPI) Get(_ context.Context, endpoint string) ([]json.RawMessage, error) {
	for fragment, err := range f.errors {
		if strings.Contains(endpoint, fragment) {
			return nil, err
		}
	}
	for fragment, response := range f.responses {
		if strings.Contains(endpoint, fragment) {
			return response, nil
		}
	}
	return nil, errors.New("unexpected endpoint: " + endpoint)
}

func fixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	contents, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

type runnerFunc func(context.Context, process.Request) (process.Result, error)

func (f runnerFunc) Run(ctx context.Context, request process.Request) (process.Result, error) {
	return f(ctx, request)
}

func TestGHAPIUsesReadOnlyPaginatedStructuredCall(t *testing.T) {
	var got process.Request
	client := GHAPI{Host: "github.example.com", Runner: runnerFunc(func(_ context.Context, request process.Request) (process.Result, error) {
		got = request
		return process.Result{Stdout: []byte("HTTP/2.0 200 OK\r\nX-RateLimit-Remaining: 10\r\n\r\n{\"ok\":true}\n")}, nil
	})}
	pages, err := client.Get(context.Background(), "repos/o/r/rules/branches/main")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api", "--method", "GET", "--paginate", "--include", "-H", "Accept: application/vnd.github+json", "--hostname", "github.example.com", "repos/o/r/rules/branches/main"}
	if got.Name != "gh" || !reflect.DeepEqual(got.Args, want) || len(pages) != 1 {
		t.Fatalf("request = %#v, pages = %d; want gh %#v", got, len(pages), want)
	}
}

func TestGHAPIPreservesRateLimitMetadata(t *testing.T) {
	client := GHAPI{Runner: runnerFunc(func(context.Context, process.Request) (process.Result, error) {
		return process.Result{Stdout: []byte("HTTP/2.0 429 Too Many Requests\r\nRetry-After: 7\r\n\r\n{}\n"), Stderr: []byte("gh: rate limit exceeded\n")}, errors.New("exit status 1")
	})}
	_, err := client.Get(context.Background(), "endpoint")
	var limited *RateLimitError
	if !errors.As(err, &limited) || limited.RetryAfter(time.Unix(0, 0)) != 7*time.Second {
		t.Fatalf("error = %v, want seven-second RateLimitError", err)
	}
}

func TestGHAPIUsesLatestPaginatedFailureMetadata(t *testing.T) {
	client := GHAPI{Runner: runnerFunc(func(context.Context, process.Request) (process.Result, error) {
		stdout := "HTTP/2.0 200 OK\r\nX-RateLimit-Remaining: 10\r\nX-RateLimit-Reset: 1\r\n\r\n[]\n" +
			"HTTP/2.0 429 Too Many Requests\r\nRetry-After: 9\r\n\r\n{}\n"
		return process.Result{Stdout: []byte(stdout)}, errors.New("exit status 1")
	})}
	_, err := client.Get(context.Background(), "endpoint")
	var limited *RateLimitError
	if !errors.As(err, &limited) || limited.Status != 429 || limited.RetryAfter(time.Time{}) != 9*time.Second {
		t.Fatalf("error=%v", err)
	}
}

func TestGHAPIDoesNotRetryPermission403WithResetHeader(t *testing.T) {
	client := GHAPI{Runner: runnerFunc(func(context.Context, process.Request) (process.Result, error) {
		return process.Result{Stdout: []byte("HTTP/2.0 403 Forbidden\r\nX-RateLimit-Remaining: 42\r\nX-RateLimit-Reset: 99\r\n\r\n{}\n")}, errors.New("exit status 1")
	})}
	_, err := client.Get(context.Background(), "endpoint")
	var limited *RateLimitError
	var status *HTTPStatusError
	if errors.As(err, &limited) || !errors.As(err, &status) || status.Status != 403 {
		t.Fatalf("error=%v", err)
	}
}

func TestGHAPIPreservesHTTPStatus(t *testing.T) {
	client := GHAPI{Runner: runnerFunc(func(context.Context, process.Request) (process.Result, error) {
		return process.Result{Stderr: []byte("gh: Branch not protected (HTTP 404)\n")}, errors.New("exit status 1")
	})}
	_, err := client.Get(context.Background(), "endpoint")
	var status *HTTPStatusError
	if !errors.As(err, &status) || status.Status != 404 {
		t.Fatalf("error = %v, want HTTPStatusError 404", err)
	}
}

func TestRESTProviderNormalizesAuthoritativeSnapshot(t *testing.T) {
	api := fixtureAPI{responses: map[string][]json.RawMessage{
		"required_status_checks": {fixture(t, "classic.json")},
		"rules/branches":         {fixture(t, "rules.json")},
		"check-runs":             {fixture(t, "check-runs.json")},
		"statuses":               {fixture(t, "statuses.json")},
		"actions/runs/500":       {json.RawMessage(`{"run_attempt":1}`)},
		"actions/runs/501":       {json.RawMessage(`{"run_attempt":2}`)},
	}}
	got, err := NewProvider(RESTBackend{API: api}).Discover(context.Background(), Request{Owner: "o", Repository: "r", BaseBranch: "main", SHA: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.RequirementsAuthoritative || got.SHA != "abc" {
		t.Fatalf("unexpected snapshot metadata: %+v", got)
	}
	wantContexts := []string{"ci/build", "legacy", "rules-only", "security"}
	var contexts []string
	for _, requirement := range got.Requirements {
		contexts = append(contexts, requirement.Context)
	}
	if !reflect.DeepEqual(contexts, wantContexts) {
		t.Fatalf("required contexts = %v, want %v", contexts, wantContexts)
	}
	if got.Requirements[0].AppID == nil || *got.Requirements[0].AppID != 15368 || len(got.Requirements[0].Authorities) != 2 {
		t.Fatalf("app-qualified requirement was not merged: %+v", got.Requirements[0])
	}
	if len(got.Observations) != 3 {
		t.Fatalf("observations = %d, want latest three identities/sources: %+v", len(got.Observations), got.Observations)
	}
	build := got.Observations[0]
	if build.Context != "ci/build" || build.ID != 101 || build.State != StateSuccess || build.Attempt != 2 || build.WorkflowRunID == nil || *build.WorkflowRunID != 501 {
		t.Fatalf("rerun was not normalized to latest attempt: %+v", build)
	}
	legacy := got.Observations[1]
	if legacy.Context != "legacy" || legacy.ID != 202 || legacy.State != StateSuccess || legacy.AppID != nil {
		t.Fatalf("commit status was not normalized: %+v", legacy)
	}
	optional := got.Observations[2]
	if optional.State != StatePending {
		t.Fatalf("in-progress check = %q, want pending", optional.State)
	}
}

func TestProviderDistinguishesAuthoritativeZeroFromMissingSuite(t *testing.T) {
	api := fixtureAPI{responses: map[string][]json.RawMessage{
		"rules/branches": {json.RawMessage(`[]`)},
		"check-runs":     {json.RawMessage(`{"total_count":0,"check_runs":[]}`)},
		"statuses":       {json.RawMessage(`[]`)},
	}, errors: map[string]error{"required_status_checks": ErrNoClassicProtection}}
	got, err := NewProvider(RESTBackend{API: api}).Discover(context.Background(), Request{Owner: "o", Repository: "r", BaseBranch: "main", SHA: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.RequirementsAuthoritative || len(got.Requirements) != 0 || len(got.Observations) != 0 {
		t.Fatalf("expected proven empty configuration, got %+v", got)
	}
}

func TestClassicProtectionOnlyAcceptsSpecificUnprotected404(t *testing.T) {
	backend := RESTBackend{API: fixtureAPI{errors: map[string]error{
		"required_status_checks": &HTTPStatusError{Status: 404, Body: "Not Found"},
	}}}
	_, err := backend.ClassicRequirements(context.Background(), Request{Owner: "o", Repository: "r", BaseBranch: "main"})
	if err == nil || errors.Is(err, ErrNoClassicProtection) {
		t.Fatalf("ambiguous 404 must fail safe, got %v", err)
	}

	backend.API = fixtureAPI{errors: map[string]error{
		"required_status_checks": &HTTPStatusError{Status: 404, Body: "gh: Branch not protected (HTTP 404)"},
	}}
	_, err = backend.ClassicRequirements(context.Background(), Request{Owner: "o", Repository: "r", BaseBranch: "main"})
	if !errors.Is(err, ErrNoClassicProtection) {
		t.Fatalf("specific unprotected response = %v, want ErrNoClassicProtection", err)
	}
}

func TestProviderFailsSafeWhenRulesetsCannotBeProven(t *testing.T) {
	api := fixtureAPI{errors: map[string]error{
		"required_status_checks": ErrNoClassicProtection,
		"rules/branches":         &HTTPStatusError{Status: 404, Body: "not found"},
	}}
	_, err := NewProvider(RESTBackend{API: api}).Discover(context.Background(), Request{Owner: "o", Repository: "r", BaseBranch: "main", SHA: "abc"})
	if err == nil || !strings.Contains(err.Error(), "required to prove configuration") {
		t.Fatalf("expected safe ruleset failure, got %v", err)
	}
}

func TestStateNormalization(t *testing.T) {
	checks := []struct {
		status, conclusion string
		want               State
	}{
		{"queued", "", StatePending}, {"in_progress", "", StatePending}, {"completed", "success", StateSuccess},
		{"completed", "failure", StateFailure}, {"completed", "cancelled", StateFailure}, {"completed", "timed_out", StateFailure},
		{"completed", "action_required", StateFailure}, {"completed", "neutral", StateNeutral}, {"completed", "skipped", StateSkipped}, {"completed", "stale", StateStale},
	}
	for _, test := range checks {
		if got := normalizeCheckRun(test.status, test.conclusion); got != test.want {
			t.Errorf("normalizeCheckRun(%q, %q) = %q, want %q", test.status, test.conclusion, got, test.want)
		}
	}
	for raw, want := range map[string]State{"pending": StatePending, "success": StateSuccess, "failure": StateFailure, "error": StateFailure} {
		if got := normalizeStatus(raw); got != want {
			t.Errorf("normalizeStatus(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestWorkflowRunMappingIsConservative(t *testing.T) {
	if got := workflowRunFromURL("https://github.com/o/r/actions/runs/123/job/9"); got == nil || *got != 123 {
		t.Fatalf("workflow run = %v", got)
	}
	if got := workflowRunFromURL("https://external.test/run/123"); got != nil {
		t.Fatalf("external URL must not be guessed: %v", *got)
	}
}
