package copilot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maxbeizer/gh-babysit/internal/agent"
	"github.com/maxbeizer/gh-babysit/internal/process"
)

const (
	DefaultCommand = "copilot"
	MinimumVersion = "1.0.96"
)

var versionPattern = regexp.MustCompile(`(?m)GitHub Copilot CLI (\d+)\.(\d+)\.(\d+)(?:[-+.]|\s|$)`)

type Adapter struct {
	Runner         process.Runner
	Command        string
	Timeout        time.Duration
	CleanupTimeout time.Duration
	TempDir        string
}

func (a Adapter) Probe(ctx context.Context) (string, error) {
	result, err := a.runner().Run(ctx, process.Request{Name: a.command(), Args: []string{"--version"}, Env: safeEnvironment()})
	if err != nil {
		return "", fmt.Errorf("run copilot --version: %w: %s", err, strings.TrimSpace(string(result.Stderr)))
	}
	match := versionPattern.FindStringSubmatch(string(result.Stdout))
	if match == nil {
		return "", fmt.Errorf("unrecognized Copilot version output %q", strings.TrimSpace(string(result.Stdout)))
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	if major != 1 || minor == 0 && patch < 96 {
		return "", fmt.Errorf("unsupported Copilot CLI %s.%s.%s; supported range is >=%s, <2.0.0", match[1], match[2], match[3], MinimumVersion)
	}
	return match[1] + "." + match[2] + "." + match[3], nil
}

func (a Adapter) Run(ctx context.Context, request agent.Request) (outcome agent.Outcome, err error) {
	runCtx := ctx
	cancel := func() {}
	if a.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, a.Timeout)
	}
	defer cancel()
	if request.SchemaVersion != 0 && request.SchemaVersion != agent.SchemaVersion {
		return outcome, fmt.Errorf("unsupported request schema version %d", request.SchemaVersion)
	}
	if request.Mode != agent.Diagnose && request.Mode != agent.Fix {
		return outcome, fmt.Errorf("unsupported agent mode %q", request.Mode)
	}
	if _, err = a.Probe(runCtx); err != nil {
		return outcome, err
	}
	workspace, cleanup, err := a.isolatedWorkspace(runCtx, request)
	if err != nil {
		return outcome, err
	}
	if cleanup != nil {
		defer func() {
			err = errors.Join(err, cleanup())
		}()
	}
	request.SchemaVersion = agent.SchemaVersion
	request.Workspace = workspace
	prompt, err := BuildPrompt(request)
	if err != nil {
		return outcome, err
	}
	args := []string{"-C", workspace, "-p", prompt, "--silent", "--stream", "off", "--no-ask-user", "--no-auto-update", "--no-custom-instructions", "--disable-builtin-mcps", "--disallow-temp-dir", "--output-format", "text", "--available-tools", "glob", "view"}
	if request.Mode == agent.Fix {
		// The explicit allowlist excludes command execution and every MCP tool.
		args = append(args, "apply_patch", "--allow-tool=write")
	}
	result, runErr := a.runner().Run(runCtx, process.Request{Name: a.command(), Args: args, Dir: workspace, Env: safeEnvironment()})
	if runErr != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return outcome, fmt.Errorf("copilot timed out: %w", runCtx.Err())
		}
		if runCtx.Err() != nil {
			return outcome, fmt.Errorf("copilot canceled: %w", runCtx.Err())
		}
		return outcome, fmt.Errorf("copilot failed: %w: %s", runErr, strings.TrimSpace(string(result.Stderr)))
	}
	return Normalize(result.Stdout)
}

func (a Adapter) isolatedWorkspace(ctx context.Context, request agent.Request) (string, func() error, error) {
	if request.Mode == agent.Fix {
		return request.Workspace, nil, nil
	}
	before, err := a.repositoryIdentity(ctx, request.Workspace)
	if err != nil {
		return "", nil, err
	}
	if before.status != "" {
		return "", nil, errors.New("diagnosis requires a clean workspace")
	}
	temp, err := os.MkdirTemp(a.TempDir, "gh-babysit-diagnosis-")
	if err != nil {
		return "", nil, fmt.Errorf("create diagnosis directory: %w", err)
	}
	if err := a.git(ctx, request.Workspace, "worktree", "add", "--detach", temp, request.RecordedSHA); err != nil {
		_ = os.RemoveAll(temp)
		return "", nil, err
	}
	cleanup := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cleanupTimeout())
		defer cancel()
		removeErr := a.git(cleanupCtx, request.Workspace, "worktree", "remove", "--force", temp)
		_ = os.RemoveAll(temp)
		after, identityErr := a.repositoryIdentity(cleanupCtx, request.Workspace)
		if identityErr != nil {
			return identityErr
		}
		if after != before {
			return errors.New("real workspace changed during diagnosis")
		}
		return removeErr
	}
	return filepath.Clean(temp), cleanup, nil
}

type repositoryState struct{ head, status string }

func (a Adapter) repositoryIdentity(ctx context.Context, dir string) (repositoryState, error) {
	head, err := a.gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return repositoryState{}, err
	}
	status, err := a.gitOutput(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	return repositoryState{strings.TrimSpace(head), status}, err
}

func (a Adapter) git(ctx context.Context, dir string, args ...string) error {
	_, err := a.gitResult(ctx, dir, args...)
	return err
}
func (a Adapter) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	result, err := a.gitResult(ctx, dir, args...)
	return string(result.Stdout), err
}
func (a Adapter) gitResult(ctx context.Context, dir string, args ...string) (process.Result, error) {
	result, err := a.runner().Run(ctx, process.Request{Name: "git", Args: args, Dir: dir, Env: safeEnvironment()})
	if err != nil {
		return result, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(result.Stderr)))
	}
	return result, nil
}
func (a Adapter) runner() process.Runner {
	if a.Runner != nil {
		return a.Runner
	}
	return process.ExecRunner{}
}
func (a Adapter) cleanupTimeout() time.Duration {
	if a.CleanupTimeout > 0 {
		return a.CleanupTimeout
	}
	return 30 * time.Second
}
func (a Adapter) command() string {
	if a.Command != "" {
		return a.Command
	}
	return DefaultCommand
}
func safeEnvironment() []string {
	blocked := map[string]bool{"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true, "GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "COPILOT_ALLOW_ALL": true}
	env := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if !blocked[key] {
			env = append(env, item)
		}
	}
	return append(env, "COPILOT_AUTO_UPDATE=false", "USE_TGREP=false")
}
