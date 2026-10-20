package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/maxbeizer/gh-babysit/internal/exitcode"
	"github.com/maxbeizer/gh-babysit/internal/process"
)

type runnerFunc func(context.Context, process.Request) (process.Result, error)

func (f runnerFunc) Run(ctx context.Context, r process.Request) (process.Result, error) {
	return f(ctx, r)
}

func successfulPreflightRunner(_ context.Context, r process.Request) (process.Result, error) {
	command := r.Name + " " + strings.Join(r.Args, " ")
	outputs := map[string]string{
		"git rev-parse --show-toplevel": "/repo\n", "git remote get-url origin": "git@github.com:acme/widgets.git\n",
		"git status --porcelain=v1 -z --untracked-files=all": "", "gh repo view --json nameWithOwner,url,viewerPermission": `{"nameWithOwner":"acme/widgets","viewerPermission":"WRITE"}`,
		"gh pr view 123 --json number,url,baseRefName,headRefName,headRefOid,headRepository,headRepositoryOwner,isCrossRepository": `{"number":123,"url":"https://github.com/acme/widgets/pull/123","baseRefName":"main","headRefName":"topic","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","headRepository":{"name":"widgets"},"headRepositoryOwner":{"login":"acme"}}`,
		"git symbolic-ref --quiet --short HEAD": "topic\n", "git rev-parse HEAD": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n",
		"git config --get branch.topic.remote": "origin\n", "git rev-parse --abbrev-ref --symbolic-full-name @{push}": "origin/topic\n",
		"git remote get-url --push origin": "git@github.com:acme/widgets.git\n", "git ls-files -s -z": "index\x00",
	}
	if command == "gh auth status --hostname github.com" {
		return process.Result{}, nil
	}
	if command == "git config --get branch.topic.pushRemote" || command == "git config --get remote.pushDefault" {
		return process.Result{}, fmt.Errorf("unset")
	}
	if command == "gh pr view 123 --repo acme/widgets --json headRefOid" {
		return process.Result{Stdout: []byte(`{"headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)}, nil
	}
	if strings.Contains(command, "required_status_checks") {
		return process.Result{Stdout: []byte("HTTP/2.0 200 OK\n\n{\"contexts\":[],\"checks\":[]}")}, nil
	}
	if strings.Contains(command, "rules/branches") || strings.Contains(command, "/statuses?") {
		return process.Result{Stdout: []byte("HTTP/2.0 200 OK\n\n[]")}, nil
	}
	if strings.Contains(command, "/check-runs?") {
		return process.Result{Stdout: []byte("HTTP/2.0 200 OK\n\n{\"check_runs\":[]}")}, nil
	}
	out, ok := outputs[command]
	if !ok {
		return process.Result{}, fmt.Errorf("unexpected command %s", command)
	}
	return process.Result{Stdout: []byte(out)}, nil
}

func TestParsePullRequestArgument(t *testing.T) {
	tests := []struct {
		value   string
		number  int
		url     string
		wantErr bool
	}{
		{value: "42", number: 42},
		{value: "https://github.example.com/owner/repo/pull/42", url: "https://github.example.com/owner/repo/pull/42"},
		{value: "0", wantErr: true},
		{value: "branch-name", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := ParsePullRequestArgument(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParsePullRequestArgument() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got.Number != tt.number || got.URL != tt.url {
				t.Fatalf("ParsePullRequestArgument() = %#v", got)
			}
		})
	}
}

func TestCommandAcceptsDocumentedSurface(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommandWithRunner("dev", runnerFunc(successfulPreflightRunner))
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"123", "--auto", "--agent-command", "agent", "--poll-interval", "1ms", "--appearance-timeout", "2s", "--timeout", "3s"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Watching required checks") || !strings.Contains(out.String(), "success (stable 2/2)") {
		t.Fatalf("output = %q", out.String())
	}
}
func TestCommandPreservesWatchTimeAuthenticationClassification(t *testing.T) {
	runner := runnerFunc(func(ctx context.Context, request process.Request) (process.Result, error) {
		command := request.Name + " " + strings.Join(request.Args, " ")
		if strings.Contains(command, "rules/branches") {
			return process.Result{Stdout: []byte("HTTP/2.0 401 Unauthorized\n\n{}\n"), Stderr: []byte("gh: authentication required\n")}, fmt.Errorf("exit status 1")
		}
		return successfulPreflightRunner(ctx, request)
	})
	cmd := NewCommandWithRunner("dev", runner)
	cmd.SetArgs([]string{"123", "--poll-interval", "1ms", "--timeout", "1s"})
	err := cmd.Execute()
	if err == nil || exitcode.Code(err) != 2 {
		t.Fatalf("error=%v code=%d", err, exitcode.Code(err))
	}
}

func TestCommandRejectsExtraArguments(t *testing.T) {
	cmd := NewCommand("dev")
	cmd.SetArgs([]string{"1", "2"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected argument error")
	}
}
func TestHelpNamesCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand("dev")
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "gh-babysit [<pr-number-or-url>]") {
		t.Fatalf("help = %q", out.String())
	}
}
