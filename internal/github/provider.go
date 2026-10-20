// Package github provides the read-only GitHub boundary used by the watcher.
package github

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Source identifies the GitHub object that produced an observation.
type Source string

const (
	SourceCheckRun     Source = "check_run"
	SourceCommitStatus Source = "commit_status"
)

// State is the watcher-facing, source-independent state of an observation.
type State string

const (
	StatePending State = "pending"
	StateSuccess State = "success"
	StateFailure State = "failure"
	StateNeutral State = "neutral"
	StateSkipped State = "skipped"
	StateStale   State = "stale"
)

// Identity is a configured status context, optionally qualified by a GitHub App.
// A nil AppID is deliberately not treated as equivalent to an app-qualified identity.
type Identity struct {
	Context string `json:"context"`
	AppID   *int64 `json:"app_id,omitempty"`
}

// Requirement is an identity GitHub says is required for the target branch.
type Requirement struct {
	Identity
	Authorities []string `json:"authorities"`
}

// Observation describes one check run or commit status on the exact requested SHA.
type Observation struct {
	Identity
	Source             Source     `json:"source"`
	ID                 int64      `json:"id"`
	State              State      `json:"state"`
	RawState           string     `json:"raw_state"`
	Conclusion         string     `json:"conclusion,omitempty"`
	WorkflowConclusion string     `json:"workflow_conclusion,omitempty"`
	URL                string     `json:"url,omitempty"`
	WorkflowRunID      *int64     `json:"workflow_run_id,omitempty"`
	Attempt            int        `json:"attempt,omitempty"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

// Snapshot contains authoritative configuration and observations for one commit.
// RequirementsAuthoritative is always true for a successful Discover call; failures
// are returned rather than represented as an empty configuration.
type Snapshot struct {
	Repository                string        `json:"repository"`
	BaseBranch                string        `json:"base_branch"`
	SHA                       string        `json:"sha"`
	RequirementsAuthoritative bool          `json:"requirements_authoritative"`
	IndeterminateReason       string        `json:"indeterminate_reason,omitempty"`
	Requirements              []Requirement `json:"requirements"`
	Observations              []Observation `json:"observations"`
}

type Request struct {
	Owner, Repository, BaseBranch, SHA string
	PullRequestNumber                  int
}

// Backend separates transport from normalization and is intentionally exact-SHA
// addressed. Implementations must return ErrNoClassicProtection only for the REST
// branch-protection 404; ruleset errors must remain errors.
type Backend interface {
	ClassicRequirements(context.Context, Request) ([]Requirement, error)
	RulesetRequirements(context.Context, Request) ([]Requirement, error)
	Observations(context.Context, Request) ([]Observation, error)
}

var ErrNoClassicProtection = errors.New("branch has no classic protection")

type PullRequestRequirementResult struct {
	Requirements            []Requirement
	CompletionAuthoritative bool
}

type PullRequestRequirements interface {
	Requirements(context.Context, Request) (PullRequestRequirementResult, error)
}

type Provider struct {
	backend  Backend
	fallback PullRequestRequirements
}

func NewProvider(backend Backend) *Provider { return &Provider{backend: backend} }

func NewHybridProvider(backend Backend, fallback PullRequestRequirements) *Provider {
	return &Provider{backend: backend, fallback: fallback}
}

func (p *Provider) Discover(ctx context.Context, req Request) (Snapshot, error) {
	if p == nil || p.backend == nil {
		return Snapshot{}, errors.New("github provider has no backend")
	}
	if req.Owner == "" || req.Repository == "" || req.BaseBranch == "" || req.SHA == "" {
		return Snapshot{}, errors.New("owner, repository, base branch, and SHA are required")
	}
	classic, err := p.backend.ClassicRequirements(ctx, req)
	useFallback := isConfigurationPermissionError(err)
	if err != nil && !errors.Is(err, ErrNoClassicProtection) && !useFallback {
		return Snapshot{}, fmt.Errorf("read classic required checks: %w", err)
	}
	var rules []Requirement
	if !useFallback {
		rules, err = p.backend.RulesetRequirements(ctx, req)
		useFallback = isConfigurationPermissionError(err)
		if err != nil && !useFallback {
			return Snapshot{}, fmt.Errorf("read applicable rulesets (required to prove configuration): %w", err)
		}
	}
	observed, err := p.backend.Observations(ctx, req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read observations for %s: %w", req.SHA, err)
	}

	requirements := mergeRequirements(classic, rules)
	authoritative := true
	if useFallback {
		if p.fallback == nil {
			return Snapshot{}, errors.New("Codespaces token cannot read required-check configuration and no PR-scoped fallback is configured")
		}
		if req.PullRequestNumber <= 0 {
			return Snapshot{}, errors.New("pull request number is required for PR-scoped required-check fallback")
		}
		fallback, fallbackErr := p.fallback.Requirements(ctx, req)
		if fallbackErr != nil {
			return Snapshot{}, fmt.Errorf("read PR-scoped required checks after configuration permission denial: %w", fallbackErr)
		}
		requirements = fallback.Requirements
		authoritative = fallback.CompletionAuthoritative
	}
	snapshot := Snapshot{
		Repository:                req.Owner + "/" + req.Repository,
		BaseBranch:                req.BaseBranch,
		SHA:                       req.SHA,
		RequirementsAuthoritative: authoritative,
		Requirements:              requirements,
		Observations:              latestObservations(observed),
	}
	if useFallback && !authoritative {
		snapshot.IndeterminateReason = "Codespaces token can identify observed required checks but cannot prove the complete required-check set; use a token with repository Administration read permission"
	}
	return snapshot, nil
}

func isConfigurationPermissionError(err error) bool {
	if err == nil {
		return false
	}
	var limited *RateLimitError
	if errors.As(err, &limited) {
		return false
	}
	var status *HTTPStatusError
	if !errors.As(err, &status) || status.Status != 403 {
		return false
	}
	body := strings.ToLower(status.Body)
	permissions := strings.ToLower(status.Headers["x-accepted-github-permissions"])
	return strings.Contains(body, "resource not accessible by integration") &&
		strings.Contains(permissions, "administration=read")
}

func identityKey(i Identity) string {
	app := "unqualified"
	if i.AppID != nil {
		app = fmt.Sprintf("app:%d", *i.AppID)
	}
	return strings.ToLower(i.Context) + "\x00" + app
}

func mergeRequirements(groups ...[]Requirement) []Requirement {
	byKey := make(map[string]Requirement)
	for _, group := range groups {
		for _, requirement := range group {
			key := identityKey(requirement.Identity)
			current, ok := byKey[key]
			if !ok {
				current = Requirement{Identity: requirement.Identity}
			}
			for _, authority := range requirement.Authorities {
				if !contains(current.Authorities, authority) {
					current.Authorities = append(current.Authorities, authority)
				}
			}
			sort.Strings(current.Authorities)
			byKey[key] = current
		}
	}
	result := make([]Requirement, 0, len(byKey))
	for _, requirement := range byKey {
		result = append(result, requirement)
	}
	sort.Slice(result, func(i, j int) bool { return identityKey(result[i].Identity) < identityKey(result[j].Identity) })
	return result
}

func latestObservations(all []Observation) []Observation {
	latest := make(map[string]Observation)
	for _, observation := range all {
		key := identityKey(observation.Identity) + "\x00" + string(observation.Source)
		current, ok := latest[key]
		if !ok || observationNewer(observation, current) {
			latest[key] = observation
		}
	}
	result := make([]Observation, 0, len(latest))
	for _, observation := range latest {
		result = append(result, observation)
	}
	sort.Slice(result, func(i, j int) bool {
		ki := identityKey(result[i].Identity) + "\x00" + string(result[i].Source)
		kj := identityKey(result[j].Identity) + "\x00" + string(result[j].Source)
		return ki < kj
	})
	return result
}

func observationNewer(a, b Observation) bool {
	if a.Attempt != b.Attempt && (a.Attempt > 0 || b.Attempt > 0) {
		return a.Attempt > b.Attempt
	}
	at, bt := observationTime(a), observationTime(b)
	if !at.Equal(bt) {
		return at.After(bt)
	}
	return a.ID > b.ID
}

func observationTime(o Observation) time.Time {
	if o.CompletedAt != nil {
		return *o.CompletedAt
	}
	if o.StartedAt != nil {
		return *o.StartedAt
	}
	return time.Time{}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
