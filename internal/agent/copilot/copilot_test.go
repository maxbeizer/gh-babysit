package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maxbeizer/gh-babysit/internal/agent"
	"github.com/maxbeizer/gh-babysit/internal/process"
)

type call struct {
	name string
	args []string
}
type fakeRunner struct {
	calls []call
	run   func(context.Context, process.Request) (process.Result, error)
}

func (f *fakeRunner) Run(ctx context.Context, request process.Request) (process.Result, error) {
	f.calls = append(f.calls, call{request.Name, append([]string(nil), request.Args...)})
	return f.run(ctx, request)
}

func TestProbe(t *testing.T) {
	for _, tc := range []struct {
		output  string
		wantErr bool
	}{
		{"GitHub Copilot CLI 1.0.96-2.\n", false},
		{"GitHub Copilot CLI 1.2.0\n", false},
		{"GitHub Copilot CLI 1.0.95\n", true},
		{"GitHub Copilot CLI 2.0.0\n", true},
		{"unknown\n", true},
	} {
		runner := &fakeRunner{run: func(context.Context, process.Request) (process.Result, error) {
			return process.Result{Stdout: []byte(tc.output)}, nil
		}}
		_, err := (Adapter{Runner: runner}).Probe(context.Background())
		if (err != nil) != tc.wantErr {
			t.Errorf("Probe(%q) error = %v", tc.output, err)
		}
	}
}

func TestFixInvocationHasNoShellAndExplicitWritePermission(t *testing.T) {
	runner := &fakeRunner{run: func(_ context.Context, request process.Request) (process.Result, error) {
		if request.Args[0] == "--version" {
			return process.Result{Stdout: []byte("GitHub Copilot CLI 1.0.96-2.\n")}, nil
		}
		return process.Result{Stdout: []byte(validOutcome)}, nil
	}}
	_, err := (Adapter{Runner: runner}).Run(context.Background(), agent.Request{Mode: agent.Fix, Workspace: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(runner.calls[1].args, " ")
	for _, want := range []string{"--available-tools glob view apply_patch", "--allow-tool=write", "--no-ask-user", "--no-custom-instructions", "--disable-builtin-mcps"} {
		if !strings.Contains(args, want) {
			t.Errorf("argv missing %q: %s", want, args)
		}
	}
	for _, arg := range runner.calls[1].args {
		if arg == "--allow-all" || arg == "--allow-all-tools" || arg == "--allow-all-paths" || strings.HasPrefix(arg, "--allow-tool=shell") {
			t.Errorf("argv grants prohibited capability %q", arg)
		}
	}
}

func TestDiagnosisUsesDetachedWorktreeAndVerifiesRealWorkspace(t *testing.T) {
	statusCalls := 0
	runner := &fakeRunner{run: func(_ context.Context, request process.Request) (process.Result, error) {
		if request.Name == "copilot" && request.Args[0] == "--version" {
			return process.Result{Stdout: []byte("GitHub Copilot CLI 1.0.96-2.\n")}, nil
		}
		if request.Name == "copilot" {
			return process.Result{Stdout: []byte(validOutcome)}, nil
		}
		if request.Args[0] == "rev-parse" {
			return process.Result{Stdout: []byte("abc\n")}, nil
		}
		if request.Args[0] == "status" {
			statusCalls++
			return process.Result{}, nil
		}
		return process.Result{}, nil
	}}
	_, err := (Adapter{Runner: runner, TempDir: t.TempDir()}).Run(context.Background(), agent.Request{Mode: agent.Diagnose, Workspace: "/real", RecordedSHA: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, call := range runner.calls {
		joined += call.name + " " + strings.Join(call.args, " ") + "\n"
	}
	if !strings.Contains(joined, "git worktree add --detach") || !strings.Contains(joined, "git worktree remove --force") {
		t.Fatalf("missing worktree isolation calls:\n%s", joined)
	}
	if statusCalls != 2 {
		t.Fatalf("workspace status checked %d times, want 2", statusCalls)
	}
	if strings.Contains(joined, "apply_patch") || strings.Contains(joined, "--allow-tool=write") {
		t.Fatal("diagnosis was given write capability")
	}
}

func TestCleanupFailureIsJoinedWithInvocationFailure(t *testing.T) {
	runner := &fakeRunner{run: func(_ context.Context, request process.Request) (process.Result, error) {
		if request.Name == "copilot" && request.Args[0] == "--version" {
			return process.Result{Stdout: []byte("GitHub Copilot CLI 1.0.96\n")}, nil
		}
		if request.Name == "copilot" {
			return process.Result{}, errors.New("invocation failed")
		}
		if request.Args[0] == "rev-parse" {
			return process.Result{Stdout: []byte("abc\n")}, nil
		}
		if request.Args[0] == "status" {
			return process.Result{}, nil
		}
		if request.Args[0] == "worktree" && request.Args[1] == "remove" {
			return process.Result{}, errors.New("cleanup failed")
		}
		return process.Result{}, nil
	}}
	_, err := (Adapter{Runner: runner, TempDir: t.TempDir()}).Run(context.Background(), agent.Request{Mode: agent.Diagnose, Workspace: "/repo", RecordedSHA: "abc"})
	if err == nil || !strings.Contains(err.Error(), "invocation failed") || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("errors were not joined: %v", err)
	}
}

func TestCleanupHasIndependentTimeout(t *testing.T) {
	runner := &fakeRunner{run: func(ctx context.Context, request process.Request) (process.Result, error) {
		if request.Name == "copilot" && request.Args[0] == "--version" {
			return process.Result{Stdout: []byte("GitHub Copilot CLI 1.0.96\n")}, nil
		}
		if request.Name == "copilot" {
			return process.Result{Stdout: []byte(validOutcome)}, nil
		}
		if request.Args[0] == "rev-parse" {
			return process.Result{Stdout: []byte("abc\n")}, nil
		}
		if request.Args[0] == "status" {
			return process.Result{}, nil
		}
		if request.Args[0] == "worktree" && request.Args[1] == "remove" {
			<-ctx.Done()
			return process.Result{}, ctx.Err()
		}
		return process.Result{}, nil
	}}
	_, err := (Adapter{Runner: runner, CleanupTimeout: time.Millisecond, TempDir: t.TempDir()}).Run(context.Background(), agent.Request{Mode: agent.Diagnose, Workspace: "/repo", RecordedSHA: "abc"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestTimeoutCoversProbe(t *testing.T) {
	runner := &fakeRunner{run: func(ctx context.Context, _ process.Request) (process.Result, error) {
		<-ctx.Done()
		return process.Result{}, ctx.Err()
	}}
	_, err := (Adapter{Runner: runner, Timeout: time.Millisecond}).Run(context.Background(), agent.Request{Mode: agent.Fix, Workspace: "/repo"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestTimeoutCoversWorktreeSetup(t *testing.T) {
	runner := &fakeRunner{run: func(ctx context.Context, request process.Request) (process.Result, error) {
		if request.Name == "copilot" {
			return process.Result{Stdout: []byte("GitHub Copilot CLI 1.0.96\n")}, nil
		}
		if request.Args[0] == "rev-parse" {
			return process.Result{Stdout: []byte("abc\n")}, nil
		}
		if request.Args[0] == "status" || request.Args[0] == "worktree" && request.Args[1] == "list" {
			return process.Result{}, nil
		}
		<-ctx.Done()
		return process.Result{}, ctx.Err()
	}}
	_, err := (Adapter{Runner: runner, Timeout: time.Millisecond, TempDir: t.TempDir()}).Run(context.Background(), agent.Request{Mode: agent.Diagnose, Workspace: "/repo", RecordedSHA: "abc"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestCanceledRunnerExitStillPreservesDeadline(t *testing.T) {
	runner := &fakeRunner{run: func(ctx context.Context, _ process.Request) (process.Result, error) {
		<-ctx.Done()
		return process.Result{}, errors.New("process killed")
	}}
	_, err := (Adapter{Runner: runner, Timeout: time.Millisecond}).Run(context.Background(), agent.Request{Mode: agent.Fix, Workspace: "/repo"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline cause lost: %v", err)
	}
}

func TestPartialWorktreeRegistrationIsRemoved(t *testing.T) {
	removed := false
	var runner *fakeRunner
	runner = &fakeRunner{run: func(ctx context.Context, request process.Request) (process.Result, error) {
		if request.Name == "copilot" {
			return process.Result{Stdout: []byte("GitHub Copilot CLI 1.0.96\n")}, nil
		}
		if request.Args[0] == "rev-parse" {
			return process.Result{Stdout: []byte("abc\n")}, nil
		}
		if request.Args[0] == "status" {
			return process.Result{}, nil
		}
		if request.Args[0] == "worktree" && request.Args[1] == "add" {
			<-ctx.Done()
			return process.Result{}, errors.New("process killed")
		}
		if request.Args[0] == "worktree" && request.Args[1] == "list" {
			return process.Result{Stdout: []byte("worktree " + request.Dir + "/elsewhere\n\nworktree " + findTempPath(runner.calls) + "\n")}, nil
		}
		if request.Args[0] == "worktree" && request.Args[1] == "remove" {
			removed = true
			return process.Result{}, nil
		}
		return process.Result{}, nil
	}}
	_, err := (Adapter{Runner: runner, Timeout: time.Millisecond, TempDir: t.TempDir()}).Run(context.Background(), agent.Request{Mode: agent.Diagnose, Workspace: "/repo", RecordedSHA: "abc"})
	if !errors.Is(err, context.DeadlineExceeded) || !removed {
		t.Fatalf("err=%v removed=%v", err, removed)
	}
}

func findTempPath(calls []call) string {
	for _, call := range calls {
		if call.name == "git" && len(call.args) >= 5 && call.args[0] == "worktree" && call.args[1] == "add" {
			return call.args[3]
		}
	}
	return ""
}

func TestTimeoutIsDeterministic(t *testing.T) {
	runner := &fakeRunner{run: func(ctx context.Context, request process.Request) (process.Result, error) {
		if request.Args[0] == "--version" {
			return process.Result{Stdout: []byte("GitHub Copilot CLI 1.0.96\n")}, nil
		}
		<-ctx.Done()
		return process.Result{}, ctx.Err()
	}}
	_, err := (Adapter{Runner: runner, Timeout: time.Millisecond}).Run(context.Background(), agent.Request{Mode: agent.Fix, Workspace: "/repo"})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v", err)
	}
}
