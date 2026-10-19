package github

import (
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
	Runner   process.Runner
	Root     string
	PR       domain.PullRequest
	Provider *Provider
}

func NewWatchSource(runner process.Runner, root string, pr domain.PullRequest) (*WatchSource, error) {
	parts := strings.Split(pr.Repository, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repository %q", pr.Repository)
	}
	api := GHAPI{Runner: runner, Host: pr.Host}
	return &WatchSource{Runner: runner, Root: root, PR: pr, Provider: NewProvider(RESTBackend{API: api})}, nil
}

func (s *WatchSource) HeadSHA(ctx context.Context) (string, error) {
	result, err := s.Runner.Run(ctx, process.Request{Name: "gh", Dir: s.Root, Args: []string{"pr", "view", strconv.Itoa(s.PR.Number), "--repo", s.PR.Repository, "--json", "headRefOid"}})
	if err != nil {
		return "", fmt.Errorf("gh pr view: %w: %s", err, strings.TrimSpace(string(result.Stderr)))
	}
	var response struct {
		HeadRefOID string `json:"headRefOid"`
	}
	if err := json.Unmarshal(result.Stdout, &response); err != nil || response.HeadRefOID == "" {
		return "", errors.New("gh pr view omitted headRefOid")
	}
	return response.HeadRefOID, nil
}

func (s *WatchSource) Snapshot(ctx context.Context) (Snapshot, error) {
	parts := strings.Split(s.PR.Repository, "/")
	return s.Provider.Discover(ctx, Request{Owner: parts[0], Repository: parts[1], BaseBranch: s.PR.BaseBranch, SHA: s.PR.HeadSHA})
}

func (s *WatchSource) FailedLogs(ctx context.Context, observation Observation, _ int) (string, error) {
	if observation.WorkflowRunID == nil {
		return "", errors.New("failed check is not unambiguously mapped to a GitHub Actions run")
	}
	result, err := s.Runner.Run(ctx, process.Request{Name: "gh", Dir: s.Root, Args: []string{"run", "view", strconv.FormatInt(*observation.WorkflowRunID, 10), "--repo", s.PR.Repository, "--log-failed"}})
	if err != nil {
		return "", fmt.Errorf("gh run view: %w: %s", err, strings.TrimSpace(string(result.Stderr)))
	}
	if len(result.Stdout) == 0 {
		return "", errors.New("failed-check logs are empty or unavailable")
	}
	return string(result.Stdout), nil
}
