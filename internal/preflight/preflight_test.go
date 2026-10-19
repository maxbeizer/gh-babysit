package preflight

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/maxbeizer/gh-babysit/internal/exitcode"
	"github.com/maxbeizer/gh-babysit/internal/process"
)

type response struct {
	out string
	err error
}
type fakeRunner struct {
	responses map[string]response
	calls     []process.Request
}

func (f *fakeRunner) Run(_ context.Context, r process.Request) (process.Result, error) {
	f.calls = append(f.calls, r)
	x, ok := f.responses[key(r.Name, r.Args...)]
	if !ok {
		return process.Result{}, fmt.Errorf("unexpected command: %s", key(r.Name, r.Args...))
	}
	return process.Result{Stdout: []byte(x.out), Stderr: []byte("detail")}, x.err
}
func key(name string, args ...string) string { return name + " " + strings.Join(args, " ") }

func goodResponses() map[string]response {
	return map[string]response{
		key("git", "rev-parse", "--show-toplevel"):                                {out: "/repo\n"},
		key("git", "remote", "get-url", "origin"):                                 {out: "git@github.com:acme/widgets.git\n"},
		key("gh", "auth", "status", "--hostname", "github.com"):                   {},
		key("git", "status", "--porcelain=v1", "-z", "--untracked-files=all"):     {},
		key("gh", "repo", "view", "--json", "nameWithOwner,url,viewerPermission"): {out: `{"nameWithOwner":"acme/widgets","url":"https://github.com/acme/widgets","viewerPermission":"WRITE"}`},
		key("gh", "pr", "view", "12", "--json", "number,url,baseRefName,headRefName,headRefOid,headRepository,headRepositoryOwner,isCrossRepository"): {out: `{"number":12,"url":"https://github.com/acme/widgets/pull/12","baseRefName":"main","headRefName":"topic","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","headRepository":{"name":"widgets"},"headRepositoryOwner":{"login":"acme"},"isCrossRepository":false}`},
		key("git", "symbolic-ref", "--quiet", "--short", "HEAD"): {out: "topic\n"},
		key("git", "rev-parse", "HEAD"):                          {out: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"},
		key("git", "config", "--get", "branch.topic.pushRemote"): {err: errors.New("unset")},
		key("git", "config", "--get", "remote.pushDefault"):      {err: errors.New("unset")},
		key("git", "config", "--get", "branch.topic.remote"):     {out: "origin\n"},
		key("git", "remote", "get-url", "--push", "origin"):      {out: "https://github.com/acme/widgets.git\n"},
		key("git", "ls-files", "-s", "-z"):                       {out: "100644 abc 0\tREADME.md\x00"},
	}
}

func TestRunRecordsVerifiedBaseline(t *testing.T) {
	f := &fakeRunner{responses: goodResponses()}
	got, err := (Checker{Runner: f}).Run(context.Background(), "12")
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "topic" || got.PushRemote != "origin" || got.PullRequest.Number != 12 || got.Baseline.HeadSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || len(got.Baseline.Index) == 0 {
		t.Fatalf("result = %#v", got)
	}
	if f.calls[0].Dir != "" || f.calls[1].Dir != "/repo" {
		t.Fatalf("command directories not rooted safely: %#v", f.calls[:2])
	}
}

func TestCurrentBranchResolutionOmitsArgument(t *testing.T) {
	rs := goodResponses()
	explicit := key("gh", "pr", "view", "12", "--json", "number,url,baseRefName,headRefName,headRefOid,headRepository,headRepositoryOwner,isCrossRepository")
	current := key("gh", "pr", "view", "--json", "number,url,baseRefName,headRefName,headRefOid,headRepository,headRepositoryOwner,isCrossRepository")
	rs[current] = rs[explicit]
	delete(rs, explicit)
	if _, err := (Checker{Runner: &fakeRunner{responses: rs}}).Run(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestSafetyRejectionsAreTypedAndActionable(t *testing.T) {
	tests := []struct {
		name, command string
		response      response
		kind          Kind
	}{
		{"dirty", key("git", "status", "--porcelain=v1", "-z", "--untracked-files=all"), response{out: " M README.md\x00?? new.txt\x00"}, DirtyWorktree},
		{"fork", key("gh", "pr", "view", "12", "--json", "number,url,baseRefName,headRefName,headRefOid,headRepository,headRepositoryOwner,isCrossRepository"), response{out: `{"number":12,"url":"u","baseRefName":"main","headRefName":"topic","headRefOid":"abc","headRepository":{"name":"widgets"},"headRepositoryOwner":{"login":"other"},"isCrossRepository":true}`}, ForkPullRequest},
		{"detached", key("git", "symbolic-ref", "--quiet", "--short", "HEAD"), response{err: errors.New("detached")}, DetachedHead},
		{"head mismatch", key("git", "rev-parse", "HEAD"), response{out: "bbbb\n"}, StateMismatch},
		{"wrong push remote", key("git", "remote", "get-url", "--push", "origin"), response{out: "git@github.com:acme/other.git\n"}, UnsafeRemote},
		{"permission", key("gh", "repo", "view", "--json", "nameWithOwner,url,viewerPermission"), response{out: `{"nameWithOwner":"acme/widgets","viewerPermission":"READ"}`}, PermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs := goodResponses()
			rs[tt.command] = tt.response
			_, err := (Checker{Runner: &fakeRunner{responses: rs}}).Run(context.Background(), "12")
			var pe *Error
			if !errors.As(err, &pe) || pe.Kind != tt.kind {
				t.Fatalf("error = %v", err)
			}
			if pe.Action == "" {
				t.Fatal("missing next action")
			}
			if exitcode.Code(err) != 5 {
				t.Fatalf("exit code = %d", exitcode.Code(err))
			}
		})
	}
}

func TestAuthenticationFailureIsUsageError(t *testing.T) {
	rs := goodResponses()
	rs[key("gh", "auth", "status", "--hostname", "github.com")] = response{err: errors.New("not logged in")}
	_, err := (Checker{Runner: &fakeRunner{responses: rs}}).Run(context.Background(), "12")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != Authentication || exitcode.Code(err) != 2 || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("error = %v", err)
	}
}

func TestDirtySummary(t *testing.T) {
	got := DirtySummary([]byte(" M one\x00R  new\x00old\x00?? three\x00?? four\x00"), 2)
	want := "one, old -> new (and 2 more)"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}
