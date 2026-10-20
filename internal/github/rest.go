package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maxbeizer/gh-babysit/internal/process"
)

// API is the small read-only REST transport consumed by RESTBackend.
type API interface {
	Get(context.Context, string) ([]json.RawMessage, error)
}

// HTTPStatusError preserves status semantics needed to distinguish the one safe 404.
type HTTPStatusError struct {
	Status  int
	Body    string
	Headers map[string]string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("GitHub API returned HTTP %d: %s", e.Status, e.Body)
}

// IsAuthenticationError reports whether a watch-time GitHub failure requires reauthentication.
func IsAuthenticationError(err error) bool {
	var status *HTTPStatusError
	return errors.As(err, &status) && status.Status == 401
}

// RateLimitError carries GitHub's server-provided retry timing.
type RateLimitError struct {
	*HTTPStatusError
	RetryAfterDuration time.Duration
	ResetAt            time.Time
}

func (e *RateLimitError) RetryAfter(now time.Time) time.Duration {
	if e.RetryAfterDuration > 0 {
		return e.RetryAfterDuration
	}
	return max(e.ResetAt.Sub(now), 0)
}

// GHAPI invokes authenticated `gh api`. It never uses mutating HTTP methods.
type GHAPI struct {
	Runner process.Runner
	Host   string
}

func (c GHAPI) Get(ctx context.Context, endpoint string) ([]json.RawMessage, error) {
	if c.Runner == nil {
		return nil, errors.New("gh API runner is nil")
	}
	args := []string{"api", "--method", "GET", "--paginate", "--include", "-H", "Accept: application/vnd.github+json"}
	if c.Host != "" {
		args = append(args, "--hostname", c.Host)
	}
	args = append(args, endpoint)
	result, err := c.Runner.Run(ctx, process.Request{Name: "gh", Args: args})
	if err != nil {
		message := strings.TrimSpace(string(append(append([]byte(nil), result.Stdout...), result.Stderr...)))
		if status, response := latestHTTPResponse(message); status != 0 {
			httpErr := &HTTPStatusError{Status: status, Body: message, Headers: responseHeaders(response)}
			if retry, reset, ok := rateLimitMetadata(response); ok && isRateLimited(status, response) {
				return nil, &RateLimitError{HTTPStatusError: httpErr, RetryAfterDuration: retry, ResetAt: reset}
			}
			return nil, httpErr
		}
		return nil, fmt.Errorf("gh api %s: %w: %s", endpoint, err, message)
	}
	pages, err := parseIncludedResponses(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("decode gh api response for %s: %w", endpoint, err)
	}
	return pages, nil
}

type RESTBackend struct{ API API }

func (b RESTBackend) ClassicRequirements(ctx context.Context, req Request) ([]Requirement, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/branches/%s/protection/required_status_checks", escape(req.Owner), escape(req.Repository), escape(req.BaseBranch))
	pages, err := b.API.Get(ctx, endpoint)
	if err != nil {
		var status *HTTPStatusError
		if errors.As(err, &status) && status.Status == 404 && strings.Contains(strings.ToLower(status.Body), "branch not protected") {
			return nil, ErrNoClassicProtection
		}
		return nil, err
	}
	var response struct {
		Contexts []string `json:"contexts"`
		Checks   []struct {
			Context string `json:"context"`
			AppID   *int64 `json:"app_id"`
		} `json:"checks"`
	}
	if err := decodeSingle(pages, &response); err != nil {
		return nil, err
	}
	result := make([]Requirement, 0, len(response.Contexts)+len(response.Checks))
	checked := make(map[string]bool)
	for _, check := range response.Checks {
		if check.Context == "" {
			return nil, errors.New("classic protection returned an empty check context")
		}
		result = append(result, Requirement{Identity: Identity{Context: check.Context, AppID: check.AppID}, Authorities: []string{"classic_branch_protection"}})
		checked[strings.ToLower(check.Context)] = true
	}
	for _, contextName := range response.Contexts {
		// GitHub currently mirrors checks into contexts. Keep only genuinely legacy entries.
		if !checked[strings.ToLower(contextName)] {
			result = append(result, Requirement{Identity: Identity{Context: contextName}, Authorities: []string{"classic_branch_protection"}})
		}
	}
	return result, nil
}

func (b RESTBackend) RulesetRequirements(ctx context.Context, req Request) ([]Requirement, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/rules/branches/%s", escape(req.Owner), escape(req.Repository), escape(req.BaseBranch))
	pages, err := b.API.Get(ctx, endpoint)
	if err != nil {
		// A 404 here can mean unsupported API, hidden rulesets, or insufficient access.
		// It must not be converted into an authoritative empty configuration.
		return nil, err
	}
	var rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			Required []struct {
				Context       string `json:"context"`
				IntegrationID *int64 `json:"integration_id"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := decodeArrayPages(pages, &rules); err != nil {
		return nil, err
	}
	var result []Requirement
	for _, rule := range rules {
		if rule.Type != "required_status_checks" {
			continue
		}
		for _, check := range rule.Parameters.Required {
			if check.Context == "" {
				return nil, errors.New("ruleset returned an empty required check context")
			}
			result = append(result, Requirement{Identity: Identity{Context: check.Context, AppID: check.IntegrationID}, Authorities: []string{"repository_ruleset"}})
		}
	}
	return result, nil
}

func (b RESTBackend) Observations(ctx context.Context, req Request) ([]Observation, error) {
	checks, err := b.checkRuns(ctx, req)
	if err != nil {
		return nil, err
	}
	statuses, err := b.statuses(ctx, req)
	if err != nil {
		return nil, err
	}
	return append(checks, statuses...), nil
}

func (b RESTBackend) checkRuns(ctx context.Context, req Request) ([]Observation, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/commits/%s/check-runs?per_page=100", escape(req.Owner), escape(req.Repository), escape(req.SHA))
	pages, err := b.API.Get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var result []Observation
	workflowRuns := make(map[int64]workflowRunMetadata)
	for _, page := range pages {
		var response struct {
			CheckRuns []struct {
				ID          int64  `json:"id"`
				Name        string `json:"name"`
				Status      string `json:"status"`
				Conclusion  string `json:"conclusion"`
				DetailsURL  string `json:"details_url"`
				StartedAt   string `json:"started_at"`
				CompletedAt string `json:"completed_at"`
				App         struct {
					ID int64 `json:"id"`
				} `json:"app"`
			} `json:"check_runs"`
		}
		if err := json.Unmarshal(page, &response); err != nil {
			return nil, fmt.Errorf("decode check runs: %w", err)
		}
		for _, run := range response.CheckRuns {
			appID := run.App.ID
			workflowRunID := workflowRunFromURL(run.DetailsURL)
			metadata := workflowRunMetadata{}
			if workflowRunID != nil {
				var ok bool
				metadata, ok = workflowRuns[*workflowRunID]
				if !ok {
					metadata, err = b.workflowRun(ctx, req, *workflowRunID)
					if err != nil {
						return nil, fmt.Errorf("read workflow run %d for check run %d: %w", *workflowRunID, run.ID, err)
					}
					workflowRuns[*workflowRunID] = metadata
				}
			}
			result = append(result, Observation{
				Identity:           Identity{Context: run.Name, AppID: &appID},
				Source:             SourceCheckRun,
				ID:                 run.ID,
				State:              normalizeCheckRun(run.Status, run.Conclusion),
				RawState:           firstNonempty(run.Conclusion, run.Status),
				Conclusion:         run.Conclusion,
				WorkflowConclusion: metadata.Conclusion,
				URL:                run.DetailsURL,
				WorkflowRunID:      workflowRunID,
				Attempt:            metadata.Attempt,
				StartedAt:          parseTime(run.StartedAt), CompletedAt: parseTime(run.CompletedAt),
			})
		}
	}
	return result, nil
}

type workflowRunMetadata struct {
	Attempt    int
	Conclusion string
}

func (b RESTBackend) workflowRun(ctx context.Context, req Request, id int64) (workflowRunMetadata, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/actions/runs/%d", escape(req.Owner), escape(req.Repository), id)
	pages, err := b.API.Get(ctx, endpoint)
	if err != nil {
		return workflowRunMetadata{}, err
	}
	var run struct {
		RunAttempt int    `json:"run_attempt"`
		Conclusion string `json:"conclusion"`
	}
	if err := decodeSingle(pages, &run); err != nil {
		return workflowRunMetadata{}, err
	}
	if run.RunAttempt < 1 {
		return workflowRunMetadata{}, errors.New("workflow run omitted run_attempt")
	}
	return workflowRunMetadata{Attempt: run.RunAttempt, Conclusion: run.Conclusion}, nil
}

func (b RESTBackend) statuses(ctx context.Context, req Request) ([]Observation, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/commits/%s/statuses?per_page=100", escape(req.Owner), escape(req.Repository), escape(req.SHA))
	pages, err := b.API.Get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID        int64  `json:"id"`
		Context   string `json:"context"`
		State     string `json:"state"`
		TargetURL string `json:"target_url"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
		Creator   struct {
			ID int64 `json:"id"`
		} `json:"creator"`
	}
	if err := decodeArrayPages(pages, &raw); err != nil {
		return nil, err
	}
	result := make([]Observation, 0, len(raw))
	for _, status := range raw {
		// Commit statuses are not app-qualified. Creator IDs are user IDs, not App IDs.
		result = append(result, Observation{Identity: Identity{Context: status.Context}, Source: SourceCommitStatus, ID: status.ID, State: normalizeStatus(status.State), RawState: status.State, URL: status.TargetURL, StartedAt: parseTime(status.CreatedAt), CompletedAt: parseTime(status.UpdatedAt)})
	}
	return result, nil
}

func normalizeCheckRun(status, conclusion string) State {
	if status != "completed" {
		return StatePending
	}
	switch conclusion {
	case "success":
		return StateSuccess
	case "neutral":
		return StateNeutral
	case "skipped":
		return StateSkipped
	case "stale":
		return StateStale
	default:
		return StateFailure
	}
}

func normalizeStatus(state string) State {
	switch state {
	case "success":
		return StateSuccess
	case "failure", "error":
		return StateFailure
	default:
		return StatePending
	}
}

var (
	actionsRunPattern = regexp.MustCompile(`/actions/runs/([0-9]+)(?:/|$)`)
	ghStatusPattern   = regexp.MustCompile(`(?i)HTTP(?:/[0-9.]+)?[ :]([0-9]{3})`)
	retryAfterPattern = regexp.MustCompile(`(?im)retry-after:\s*([0-9]+)`)
	rateResetPattern  = regexp.MustCompile(`(?im)x-ratelimit-reset:\s*([0-9]+)`)
	rateRemainPattern = regexp.MustCompile(`(?im)x-ratelimit-remaining:\s*([0-9]+)`)
)

func statusFromGHError(message string) int {
	status, _ := latestHTTPResponse(message)
	return status
}

func latestHTTPResponse(message string) (int, string) {
	matches := ghStatusPattern.FindAllStringSubmatchIndex(message, -1)
	if len(matches) == 0 {
		return 0, ""
	}
	match := matches[len(matches)-1]
	status, _ := strconv.Atoi(message[match[2]:match[3]])
	return status, message[match[0]:]
}

func responseHeaders(message string) map[string]string {
	headers := make(map[string]string)
	lines := strings.Split(message, "\n")
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok {
			headers[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
		}
	}
	return headers
}

func rateLimitMetadata(message string) (time.Duration, time.Time, bool) {
	if matches := retryAfterPattern.FindAllStringSubmatch(message, -1); len(matches) > 0 {
		seconds, err := strconv.ParseInt(matches[len(matches)-1][1], 10, 64)
		if err == nil {
			return time.Duration(seconds) * time.Second, time.Time{}, true
		}
	}
	if matches := rateResetPattern.FindAllStringSubmatch(message, -1); len(matches) > 0 {
		seconds, err := strconv.ParseInt(matches[len(matches)-1][1], 10, 64)
		if err == nil {
			return 0, time.Unix(seconds, 0), true
		}
	}
	return 0, time.Time{}, false
}

func isRateLimited(status int, message string) bool {
	if status == 429 {
		return true
	}
	if status != 403 {
		return false
	}
	remaining := rateRemainPattern.FindAllStringSubmatch(message, -1)
	if len(remaining) > 0 && remaining[len(remaining)-1][1] == "0" {
		return true
	}
	lower := strings.ToLower(message)
	return strings.Contains(lower, "rate limit") || strings.Contains(lower, "secondary rate") || strings.Contains(lower, "abuse detection")
}

func workflowRunFromURL(value string) *int64 {
	match := actionsRunPattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return nil
	}
	id, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return nil
	}
	return &id
}

func parseIncludedResponses(output []byte) ([]json.RawMessage, error) {
	text := strings.ReplaceAll(string(output), "\r\n", "\n")
	var pages []json.RawMessage
	for len(strings.TrimSpace(text)) > 0 {
		text = strings.TrimLeft(text, "\n")
		if !strings.HasPrefix(text, "HTTP/") {
			return nil, errors.New("gh api --include response omitted HTTP status line")
		}
		headerEnd := strings.Index(text, "\n\n")
		if headerEnd < 0 {
			return nil, errors.New("gh api --include response omitted header terminator")
		}
		text = text[headerEnd+2:]
		next := strings.Index(text, "\nHTTP/")
		body := text
		if next >= 0 {
			body, text = text[:next], text[next+1:]
		} else {
			text = ""
		}
		body = strings.TrimSpace(body)
		if !json.Valid([]byte(body)) {
			return nil, errors.New("gh api --include response contained invalid JSON")
		}
		pages = append(pages, json.RawMessage(body))
	}
	return pages, nil
}

func decodeSingle(pages []json.RawMessage, target any) error {
	if len(pages) != 1 {
		return fmt.Errorf("expected one API page, got %d", len(pages))
	}
	return json.Unmarshal(pages[0], target)
}

func decodeArrayPages(pages []json.RawMessage, target any) error {
	combined := []byte{'['}
	first := true
	for _, page := range pages {
		var items []json.RawMessage
		if err := json.Unmarshal(page, &items); err != nil {
			return fmt.Errorf("decode API page: %w", err)
		}
		for _, item := range items {
			if !first {
				combined = append(combined, ',')
			}
			combined = append(combined, item...)
			first = false
		}
	}
	combined = append(combined, ']')
	return json.Unmarshal(combined, target)
}

func parseTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return &parsed
}
func escape(value string) string { return url.PathEscape(value) }
func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
