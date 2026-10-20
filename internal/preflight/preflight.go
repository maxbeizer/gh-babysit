package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maxbeizer/gh-babysit/internal/domain"
	"github.com/maxbeizer/gh-babysit/internal/exitcode"
	"github.com/maxbeizer/gh-babysit/internal/process"
)

type Kind string

const (
	NotWorktree      Kind = "not-worktree"
	Authentication   Kind = "authentication"
	DirtyWorktree    Kind = "dirty-worktree"
	PRResolution     Kind = "pr-resolution"
	DetachedHead     Kind = "detached-head"
	StateMismatch    Kind = "state-mismatch"
	ForkPullRequest  Kind = "fork-pull-request"
	UnsafeRemote     Kind = "unsafe-remote"
	PermissionDenied Kind = "permission-denied"
)

type Error struct {
	Kind        Kind
	PR          int
	SHA, Action string
	Err         error
}

func (e *Error) Error() string {
	var id string
	if e.PR != 0 {
		id += fmt.Sprintf(" PR #%d", e.PR)
	}
	if e.SHA != "" {
		id += " at " + e.SHA
	}
	msg := string(e.Kind) + id
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	if e.Action != "" {
		msg += "; next: " + e.Action
	}
	return msg
}
func (e *Error) Unwrap() error { return e.Err }

type Baseline struct {
	HeadSHA       string
	Status, Index []byte
}
type Result struct {
	Root, Branch, PushRemote string
	PullRequest              domain.PullRequest
	Baseline                 Baseline
}
type Checker struct{ Runner process.Runner }

func (c Checker) Run(ctx context.Context, argument string) (Result, error) {
	rootOut, err := c.run(ctx, "", "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return Result{}, usage(NotWorktree, "run this command inside the pull request's Git worktree", err)
	}
	root, err := filepath.Abs(strings.TrimSpace(string(rootOut)))
	if err != nil {
		return Result{}, usage(NotWorktree, "run this command inside a valid Git worktree", err)
	}

	remoteURL, err := c.run(ctx, root, "git", "remote", "get-url", "origin")
	if err != nil {
		return Result{}, safety(UnsafeRemote, 0, "", "configure an origin remote for this repository", err)
	}
	host, remoteRepository, err := repositoryFromRemote(strings.TrimSpace(string(remoteURL)))
	if err != nil {
		return Result{}, safety(UnsafeRemote, 0, "", "configure origin with an SSH or HTTPS GitHub URL", err)
	}
	if _, err = c.run(ctx, root, "gh", "auth", "status", "--hostname", host); err != nil {
		return Result{}, usage(Authentication, "run `gh auth login --hostname "+host+"`", err)
	}

	status, err := c.run(ctx, root, "git", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return Result{}, safety(StateMismatch, 0, "", "fix the Git status error and retry", err)
	}
	if len(status) != 0 {
		return Result{}, safety(DirtyWorktree, 0, "", "commit, stash, or remove these changes: "+DirtySummary(status, 8), nil)
	}

	repoOut, err := c.run(ctx, root, "gh", "repo", "view", host+"/"+remoteRepository, "--json", "nameWithOwner,url,viewerPermission")
	if err != nil {
		return Result{}, usage(PRResolution, "ensure this checkout has a GitHub repository and retry", err)
	}
	var repo struct{ NameWithOwner, URL, ViewerPermission string }
	if err := json.Unmarshal(repoOut, &repo); err != nil || repo.NameWithOwner == "" {
		return Result{}, usage(PRResolution, "update `gh` and retry repository resolution", err)
	}
	if repo.ViewerPermission != "ADMIN" && repo.ViewerPermission != "MAINTAIN" && repo.ViewerPermission != "WRITE" {
		return Result{}, safety(PermissionDenied, 0, "", "request write access to "+repo.NameWithOwner, nil)
	}

	prArgs := []string{"pr", "view"}
	if argument != "" {
		prArgs = append(prArgs, argument)
	} else {
		branchOut, branchErr := c.run(ctx, root, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
		branch := strings.TrimSpace(string(branchOut))
		if branchErr != nil || branch == "" {
			return Result{}, usage(PRResolution, "pass an existing PR number or URL, or check out its branch", branchErr)
		}
		// gh requires an explicit selector when --repo is used. Passing the
		// checked-out branch preserves branch-based resolution without exposing
		// gh's misleading "argument required" usage error.
		prArgs = append(prArgs, branch)
	}
	prArgs = append(prArgs, "--repo", host+"/"+remoteRepository, "--json", "number,url,baseRefName,headRefName,headRefOid,headRepository,headRepositoryOwner,isCrossRepository")
	prOut, err := c.run(ctx, root, "gh", prArgs...)
	if err != nil {
		return Result{}, usage(PRResolution, "pass an existing PR number or URL, or check out its branch", err)
	}
	var p struct {
		Number                                    int
		URL, BaseRefName, HeadRefName, HeadRefOid string
		IsCrossRepository                         bool
		HeadRepository                            struct{ Name string }
		HeadRepositoryOwner                       struct{ Login string }
	}
	if err := json.Unmarshal(prOut, &p); err != nil || p.Number == 0 || p.HeadRefOid == "" {
		return Result{}, usage(PRResolution, "update `gh` and retry PR resolution", err)
	}
	headRepo := p.HeadRepositoryOwner.Login + "/" + p.HeadRepository.Name
	pr := domain.PullRequest{Host: host, Repository: repo.NameWithOwner, Number: p.Number, URL: p.URL, BaseBranch: p.BaseRefName, HeadBranch: p.HeadRefName, HeadRepository: headRepo, HeadSHA: p.HeadRefOid}
	prHost, prRepo, parseErr := repositoryFromPullRequestURL(p.URL)
	if parseErr != nil || !strings.EqualFold(prHost, host) || !strings.EqualFold(prRepo, repo.NameWithOwner) {
		return Result{}, safety(StateMismatch, p.Number, p.HeadRefOid, "select a PR from "+host+"/"+repo.NameWithOwner, parseErr)
	}
	if p.IsCrossRepository || !strings.EqualFold(headRepo, repo.NameWithOwner) {
		return Result{}, safety(ForkPullRequest, p.Number, p.HeadRefOid, "check out a same-repository PR; fork PRs are not supported", nil)
	}

	branchOut, err := c.run(ctx, root, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return Result{}, safety(DetachedHead, p.Number, p.HeadRefOid, "check out branch "+p.HeadRefName, err)
	}
	branch := strings.TrimSpace(string(branchOut))
	headOut, err := c.run(ctx, root, "git", "rev-parse", "HEAD")
	if err != nil {
		return Result{}, safety(StateMismatch, p.Number, p.HeadRefOid, "restore a valid HEAD and retry", err)
	}
	head := strings.TrimSpace(string(headOut))
	if branch != p.HeadRefName {
		return Result{}, safety(StateMismatch, p.Number, p.HeadRefOid, "check out branch "+p.HeadRefName, fmt.Errorf("current branch is %s", branch))
	}
	if head != p.HeadRefOid {
		return Result{}, safety(StateMismatch, p.Number, p.HeadRefOid, "update the checkout to the PR head without rewriting history", fmt.Errorf("local HEAD is %s", head))
	}

	pushRemote := c.pushRemote(ctx, root, branch)
	if pushRemote == "" {
		return Result{}, safety(UnsafeRemote, p.Number, p.HeadRefOid, "configure a push remote for branch "+branch, nil)
	}
	pushRef, err := c.run(ctx, root, "git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{push}")
	if err != nil || strings.TrimSpace(string(pushRef)) != pushRemote+"/"+branch {
		return Result{}, safety(UnsafeRemote, p.Number, p.HeadRefOid, "configure the branch to push to "+pushRemote+"/"+branch, err)
	}
	pushURL, err := c.run(ctx, root, "git", "remote", "get-url", "--push", pushRemote)
	if err != nil {
		return Result{}, safety(UnsafeRemote, p.Number, p.HeadRefOid, "configure push URL for remote "+pushRemote, err)
	}
	pushHost, pushRepo, parseErr := repositoryFromRemote(strings.TrimSpace(string(pushURL)))
	if parseErr != nil || !strings.EqualFold(pushHost, host) || !strings.EqualFold(pushRepo, headRepo) {
		return Result{}, safety(UnsafeRemote, p.Number, p.HeadRefOid, "configure "+pushRemote+" to push to "+host+"/"+headRepo, parseErr)
	}

	index, err := c.run(ctx, root, "git", "ls-files", "-s", "-z")
	if err != nil {
		return Result{}, safety(StateMismatch, p.Number, p.HeadRefOid, "repair the index and retry", err)
	}
	return Result{Root: root, Branch: branch, PushRemote: pushRemote, PullRequest: pr, Baseline: Baseline{HeadSHA: head, Status: append([]byte(nil), status...), Index: index}}, nil
}

func (c Checker) pushRemote(ctx context.Context, root, branch string) string {
	for _, key := range []string{"branch." + branch + ".pushRemote", "remote.pushDefault", "branch." + branch + ".remote"} {
		if out, err := c.run(ctx, root, "git", "config", "--get", key); err == nil && strings.TrimSpace(string(out)) != "" {
			return strings.TrimSpace(string(out))
		}
	}
	return "origin"
}
func (c Checker) run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	r, err := c.Runner.Run(ctx, process.Request{Name: name, Args: args, Dir: dir})
	if err != nil {
		return r.Stdout, fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(r.Stderr)))
	}
	return r.Stdout, nil
}
func usage(k Kind, action string, err error) error {
	return exitcode.New(exitcode.Usage, &Error{Kind: k, Action: action, Err: err})
}
func safety(k Kind, pr int, sha, action string, err error) error {
	return exitcode.New(exitcode.SafetyStop, &Error{Kind: k, PR: pr, SHA: sha, Action: action, Err: err})
}

func repositoryFromPullRequestURL(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", "", fmt.Errorf("invalid pull request URL %q", raw)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return "", "", fmt.Errorf("invalid pull request URL %q", raw)
	}
	return u.Hostname(), parts[0] + "/" + parts[1], nil
}

func repositoryFromRemote(raw string) (string, string, error) {
	raw = strings.TrimSuffix(raw, ".git")
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", err
		}
		repo := strings.TrimPrefix(u.Path, "/")
		if u.Hostname() == "" || strings.Count(repo, "/") != 1 {
			return "", "", fmt.Errorf("unsupported remote %q", raw)
		}
		return u.Hostname(), repo, nil
	}
	at := strings.LastIndex(raw, "@")
	colon := strings.Index(raw, ":")
	if at < 0 || colon < at {
		return "", "", fmt.Errorf("unsupported remote %q", raw)
	}
	repo := raw[colon+1:]
	if strings.Count(repo, "/") != 1 {
		return "", "", fmt.Errorf("unsupported remote %q", raw)
	}
	return raw[at+1 : colon], repo, nil
}

func DirtySummary(status []byte, limit int) string {
	entries := strings.Split(strings.TrimSuffix(string(status), "\x00"), "\x00")
	paths := make([]string, 0, len(entries))
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		path := e[3:]
		if (e[0] == 'R' || e[0] == 'C') && i+1 < len(entries) {
			i++
			path = entries[i] + " -> " + path
		}
		paths = append(paths, path)
	}
	shown := paths
	if len(shown) > limit {
		shown = shown[:limit]
	}
	result := strings.Join(shown, ", ")
	if n := len(paths) - len(shown); n > 0 {
		result += " (and " + strconv.Itoa(n) + " more)"
	}
	return result
}
