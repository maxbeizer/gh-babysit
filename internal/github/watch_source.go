package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/maxbeizer/gh-babysit/internal/domain"
	"github.com/maxbeizer/gh-babysit/internal/process"
)

// WatchSource wires the exact-SHA provider to read-only gh commands used while polling.
type WatchSource struct {
	Runner         process.Runner
	Root           string
	PR             domain.PullRequest
	Provider       *Provider
	BaselineHead   string
	BaselineStatus []byte
	BaselineIndex  []byte
}

func NewWatchSource(runner process.Runner, root string, pr domain.PullRequest, baselineHead string, baselineStatus, baselineIndex []byte) (*WatchSource, error) {
	parts := strings.Split(pr.Repository, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repository %q", pr.Repository)
	}
	api := GHAPI{Runner: runner, Host: pr.Host}
	return &WatchSource{Runner: runner, Root: root, PR: pr, Provider: NewProvider(RESTBackend{API: api}), BaselineHead: baselineHead, BaselineStatus: append([]byte(nil), baselineStatus...), BaselineIndex: append([]byte(nil), baselineIndex...)}, nil
}

func (s *WatchSource) VerifyLocal(ctx context.Context) error {
	checks := []struct {
		args []string
		want []byte
		name string
	}{
		{args: []string{"rev-parse", "HEAD"}, want: []byte(s.BaselineHead + "\n"), name: "HEAD"},
		{args: []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}, want: s.BaselineStatus, name: "worktree"},
		{args: []string{"ls-files", "-s", "-z"}, want: s.BaselineIndex, name: "index"},
	}
	for _, check := range checks {
		result, err := s.Runner.Run(ctx, process.Request{Name: "git", Dir: s.Root, Args: check.args})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("verify local %s: %w", check.name, err)
		}
		if !bytes.Equal(result.Stdout, check.want) {
			return fmt.Errorf("safety stop: local %s changed during watch", check.name)
		}
	}
	return nil
}

func (s *WatchSource) HeadSHA(ctx context.Context) (string, error) {
	result, err := s.Runner.Run(ctx, process.Request{Name: "gh", Dir: s.Root, Args: []string{"pr", "view", strconv.Itoa(s.PR.Number), "--repo", s.qualifiedRepository(), "--json", "headRefOid,baseRefName"}})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", watchCommandError("gh pr view", result, err)
	}
	var response struct {
		HeadRefOID  string `json:"headRefOid"`
		BaseRefName string `json:"baseRefName"`
	}
	if err := json.Unmarshal(result.Stdout, &response); err != nil || response.HeadRefOID == "" || response.BaseRefName == "" {
		return "", errors.New("gh pr view omitted headRefOid or baseRefName")
	}
	if response.BaseRefName != s.PR.BaseBranch {
		return "", fmt.Errorf("safety stop: PR base changed from %s to %s", s.PR.BaseBranch, response.BaseRefName)
	}
	return response.HeadRefOID, nil
}

func (s *WatchSource) Snapshot(ctx context.Context) (Snapshot, error) {
	parts := strings.Split(s.PR.Repository, "/")
	return s.Provider.Discover(ctx, Request{Owner: parts[0], Repository: parts[1], BaseBranch: s.PR.BaseBranch, SHA: s.PR.HeadSHA})
}

func (s *WatchSource) FailedLogs(ctx context.Context, observation Observation, limit int) (string, error) {
	if observation.WorkflowRunID == nil {
		return "", errors.New("failed check is not unambiguously mapped to a GitHub Actions run")
	}
	result, err := s.Runner.Run(ctx, process.Request{Name: "gh", Dir: s.Root, Args: []string{"run", "view", strconv.FormatInt(*observation.WorkflowRunID, 10), "--repo", s.qualifiedRepository(), "--log-failed"}, StdoutLimit: limit, StderrLimit: 8 << 10})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", watchCommandError("gh run view", result, err)
	}
	if len(result.Stdout) == 0 {
		return "", errors.New("failed-check logs are empty or unavailable")
	}
	logs := string(result.Stdout)
	if result.StdoutTruncated {
		half := len(logs) / 2
		logs = logs[:half] + "\n[logs truncated]\n" + logs[half:]
	}
	return logs, nil
}

func (s *WatchSource) qualifiedRepository() string {
	if s.PR.Host == "" {
		return s.PR.Repository
	}
	return s.PR.Host + "/" + s.PR.Repository
}

func watchCommandError(operation string, result process.Result, err error) error {
	message := strings.TrimSpace(string(result.Stderr))
	status := statusFromGHError(message)
	if retry, reset, ok := rateLimitMetadata(message); ok && isRateLimited(status, message) {
		return &RateLimitError{HTTPStatusError: &HTTPStatusError{Status: status, Body: message}, RetryAfterDuration: retry, ResetAt: reset}
	}
	if status != 0 {
		return fmt.Errorf("%s: %w", operation, &HTTPStatusError{Status: status, Body: message})
	}
	return fmt.Errorf("%s: %w: %s", operation, err, message)
}
