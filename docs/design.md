# `gh babysit` v1 implementation design and roadmap

Status: proposed design for the initial implementation spike and v1.

## Goals and boundaries

`gh babysit` watches the required checks for one existing pull request and, after a required check fails, delegates diagnosis and an optional code fix to a coding agent. It owns GitHub check observation and safe local Git orchestration. It does not create, review, update, merge, or otherwise manage pull requests.

The implementation will be a Go CLI extension. GitHub Copilot CLI is the first adapter, but orchestration will depend only on a small internal agent interface.

## Command surface

```text
gh babysit [<pr-number-or-url>] [--auto]

Flags:
  --auto                  permit confident fixes, validation, commits, and pushes
  --agent-command string  agent executable (default: copilot)
  --poll-interval duration (default: 15s)
  --appearance-timeout duration (default: 10m)
  --timeout duration       overall watch timeout (default: 6h)
```

The first release should avoid flags for implementation details. A model-selection flag can be added only if the adapter spike proves it necessary. Poll and timeout values should also accept environment-driven test seams internally, without documenting unstable environment variables as public API.

### Exit codes

| Code | Meaning |
|---:|---|
| 0 | All required checks passed for the recorded head SHA |
| 2 | Usage, configuration, executable, or authentication error |
| 3 | User declined or canceled an interactive action |
| 4 | CI failure was diagnosed but is not confidently code-fixable |
| 5 | Safety stop or state conflict |
| 6 | Timeout |
| 130 | Interrupted by SIGINT |

Errors will carry a typed category; `main` alone translates categories to process exit codes. Cobra argument errors map to 2. SIGTERM/SIGHUP cancellation maps to 5 unless a conventional signal exit is adopted consistently before release.

## Architecture

Keep `main.go` limited to wiring, signals, and exit-code translation.

```text
internal/app        command construction and top-level orchestration
internal/github     PR resolution, required-check snapshots, and logs
internal/git        repository state, snapshots, commits, and pushes
internal/agent      adapter-neutral request/response contract
internal/agent/copilot
                    Copilot subprocess adapter and response normalization
internal/validate   constrained local command execution
internal/prompt     terminal consent prompts
internal/report     plain-text progress and final summaries
internal/clock      clock/sleeper test seam
```

Interfaces should be narrow and consumer-owned. Production implementations may invoke `git`, `gh`, and `copilot` as subprocesses. This keeps authentication aligned with the user's existing CLI setup and makes subprocess behavior straightforward to fake in tests. Do not parse human-formatted `gh` output when JSON or API output is available.

## Core data model

```go
type PullRequest struct {
    Host, Repository string
    Number           int
    URL              string
    BaseBranch       string
    HeadBranch       string
    HeadRepository   string
    HeadSHA          string
}

type CheckSource string // check_run or commit_status

type CheckIdentity struct {
    Context string
    AppID   *int64
}

type Check struct {
    Identity                     CheckIdentity
    Name, URL, State, Conclusion string
    Workflow, Event              string
    Source                       CheckSource
    CheckRunID, WorkflowRunID    *int64
    Attempt                      int
    StartedAt, CompletedAt       time.Time
    Required                     bool
}

type CheckSnapshot struct {
    HeadSHA  string
    Required []Check
    Optional []Check
    ObservedAt time.Time
}

type AgentMode string // diagnose or fix

type Classification string
// code_fixable, flaky_or_infrastructure, ambiguous, insufficient_evidence

type AgentOutcome struct {
    SchemaVersion      int
    Classification           Classification
    ClassificationConfidence string // high, medium, low
    FixConfidence            string // high, medium, low
    Diagnosis          string
    Plan               []string
    RelevantFiles      []string
    ValidationCommands [][]string
    FixCompleted       bool
    Summary            string
}
```

Validation commands are argument arrays, not shell strings. v1 rejects shell metacharacter-dependent recommendations rather than invoking a shell implicitly. Classification confidence and fix confidence are separate: a failure may clearly be caused by the PR while no safe fix is clear.

## Startup and checkout safety

Perform these checks in order so errors are actionable and no agent is started prematurely:

1. `git rev-parse --show-toplevel` confirms a worktree and obtains its canonical root.
2. `gh auth status --hostname <host>` confirms authentication.
3. `git status --porcelain=v1 -z --untracked-files=all` must be empty. Dirty-tree errors list paths, capped with a remaining-count summary.
4. Resolve the PR using explicit number/URL or `gh pr view` for the current branch. Never call `gh pr create`.
5. Resolve repository identity, PR head repository/branch/SHA, and URL from structured output.
6. Require the checked-out branch to equal the PR head branch, `HEAD` to equal the PR head SHA, and the checkout's push remote to target the PR head repository. v1 supports same-repository head branches only; fork PR support is deferred until remote resolution and push permissions have dedicated integration coverage.
7. Reject detached HEAD, shallow ambiguity that prevents ancestry checks, and branch configurations that would push somewhere other than the PR head.
8. Record a baseline containing HEAD, index/worktree status, and file identities.

A non-mutating push-access probe is not reliable on all servers. Use authenticated repository permission metadata when available, then treat the first normal push as authoritative. Never use `--force`, `--force-with-lease`, amend, rebase, reset, or push refspecs that rewrite the remote branch.

## Required-check discovery

### Candidate implementation

Required-check discovery is a blocking spike, independent of the Copilot spike. The internal provider is SHA-addressed and returns rich identities regardless of which GitHub API implementation wins.

The production provider is hybrid. It prefers exact-SHA REST observations plus classic branch-protection and applicable-ruleset configuration. When, and only when, a configuration read returns the integration-token permission denial `403 Resource not accessible by integration` with `X-Accepted-GitHub-Permissions: administration=read`, it uses the PR status-check rollup's `CheckRun.isRequired(pullRequestNumber:)` and `StatusContext.isRequired(pullRequestNumber:)`. Exact-SHA REST observations remain the source of rich check identity and log/rerun metadata. Authentication failures, rate limits, and unrelated permission failures do not select the fallback.

The fallback query resolves both the recorded SHA and PR head and remains inside the existing before/after PR-head bracket. `isRequired` is authoritative only for contexts present in the rollup; neither an empty nor a partially populated rollup proves that all configured checks have appeared. Completion therefore additionally requires GitHub's positive `PullRequest.mergeStateStatus=CLEAN` signal for that exact head. Pending and failing observed required checks remain actionable without that signal. If the suite is empty, or observed checks finish while merge state remains non-clean, the watcher stops safely with guidance to use a token with repository Administration read permission rather than claiming success.

Also evaluate GitHub's own required-check resolution exposed by:

```text
gh pr checks <number> --required --json bucket,completedAt,event,link,name,startedAt,state,workflow
```

Use a second unfiltered call for optional checks and final reporting. This is preferable to independently reimplementing branch-protection and ruleset matching, especially where repository rulesets supersede classic branch protection. However, the available JSON lacks app IDs, check/run IDs, attempt numbers, and source kind, so this command is a spike candidate rather than the assumed v1 foundation. Compare it with GraphQL and REST representations for exact-SHA check runs, commit statuses, branch protection, and rulesets.

Every snapshot is bracketed by structured PR reads:

1. Read the PR head SHA and require it to equal the recorded SHA.
2. Query required and all checks.
3. Read the PR head SHA again and require it to remain equal.
4. Discard the snapshot if the two reads differ; classify an unrecognized SHA as a safety stop.

`gh pr checks` is PR-oriented rather than SHA-addressed, so this bracketing is mandatory. The GitHub spike must verify its behavior for classic branch protection, repository rulesets, app-qualified required checks, fork PRs, and merge-queue-enabled repositories. If it is unreliable, implement a provider that combines the rules/branch-protection APIs with check runs and commit statuses for the exact SHA; do not silently approximate requiredness.

### Avoiding premature success

A newly pushed SHA enters an `awaiting-suite` state. It cannot pass merely because the required-check query is temporarily empty.

The authoritative expected set comes from active required-check configuration for the base branch, not from historical observations. Completion requires:

- every actively configured required identity has appeared on the current SHA;
- the required-check API reports no required pending or failing checks; and
- the same successful set is observed in two consecutive polls.

The previous SHA's completed suite is retained only as an anti-prematurity diagnostic hint. It must not gate completion because policy, workflow jobs, and matrices can legitimately change between SHAs.

An empty result is success only when the provider can positively distinguish “no required checks configured” from “configured checks have not appeared.” Otherwise it waits until `--appearance-timeout` and stops safely. This distinction is a release gate for the GitHub API spike.

Check identity uses context plus app/integration identity, not display name alone. Observations also retain source kind, check/status ID, workflow run ID, and attempt where available. Duplicate reruns collapse to the latest completed attempt for the same identity and SHA.

## State machine

```text
preflight
  -> awaiting-suite
  -> waiting-required
  -> diagnosing-failure
  -> awaiting-edit-consent (interactive)
  -> fixing
  -> validating
  -> awaiting-push-consent (interactive)
  -> committing
  -> pushing
  -> awaiting-suite (new recognized SHA)
  -> success | declined | not-fixable | safety-stop | timeout
```

At every polling and mutation boundary, verify both the PR head SHA and local repository state. Only the original SHA and SHAs pushed by this process are recognized. A remote transition to any other SHA stops immediately.

Startup performs capability checks for minimum supported `gh` and Copilot versions and for every required JSON field/flag. Unsupported or behaviorally incompatible versions fail with the detected version and supported range; version checks do not substitute for capability checks.

## Agent adapter contract

```go
type Adapter interface {
    Run(context.Context, Request) (AgentOutcome, error)
}
```

The request contains workspace/repository/PR identity, branches, recorded SHA, failed-check metadata and logs, mode, prior diagnosis for fix mode, current diff when relevant, and explicit constraints. Large logs are saved under a private temporary directory and referenced in the prompt; the adapter imposes a documented byte cap while preserving the beginning, end, and truncation notice.

The response uses a versioned JSON schema owned by `gh babysit`. Parse strictly: unknown enum values, missing required fields, malformed commands, or extra non-JSON output are adapter failures. Persist no prompts or logs unless the user explicitly requests future diagnostics functionality.

### Copilot invocation to validate in the spike

Candidate invocation:

```text
copilot -C <workspace> -p <prompt> --silent --stream off --no-ask-user \
  --no-auto-update --output-format text <permission flags>
```

Copilot's `--output-format json` is JSONL describing the session, not necessarily the babysit outcome schema. Therefore the prompt should require one schema-conforming JSON object in the final response, while a thin wrapper extracts and validates that final response.

Diagnosis mode must be technically prevented from modifying the real checkout, not merely instructed not to. The preferred spike candidate is a disposable repository snapshot or detached worktree in which incidental agent writes are harmless; destroy it afterward and verify the real workspace remained unchanged. The spike must compare snapshot cost for large repositories against verified OS/permission isolation. A read-only mount is acceptable only if Copilot remains functional without writing indexes or temporary state into the workspace.

Fix mode grants only the permissions proven necessary to inspect and edit the workspace. Explicitly deny Git commit/push/history-rewriting commands. Do not default to unrestricted `--allow-all-tools` in the shipped adapter. The orchestrator independently verifies all changes after Copilot exits.

## Fix-cycle transaction

Before fix mode, capture a cycle baseline: HEAD, index, complete status, and content hashes for relevant existing files. Attribution in a shared worktree cannot be perfect when another process edits the same file; the enforceable invariant is that observed repository state must match mutations expected across each controlled subprocess boundary. The isolation spike must decide whether fix mode edits the real checkout or a dedicated worktree whose verified patch is applied afterward. After the agent exits:

1. Require HEAD and index to be unchanged.
2. Compute changed and untracked files relative to the cycle baseline.
3. Reject changes outside the workspace, Git metadata changes, submodule state changes, ignored-file surprises relevant to validation, or files not attributable to this cycle.
4. In interactive mode, compare the changed-file set and implementation summary with the approved plan and call out material departures.
5. Run only the structured validation commands after displaying them.
6. Execute each command directly from the repository root with a minimal inherited environment, dangerous Git environment variables removed, bounded output, and an individual timeout. Reject direct `git`, `gh`, `copilot`, publishing/deployment commands, destructive filesystem tools, and commands requiring network access in v1. The initial allow policy should cover common test/build executables and package-manager test scripts; unsupported recommendations cause a safety stop rather than a shell fallback.
7. Verify HEAD, index, tracked files, and untracked files after every validation command. Stop on non-zero exit, timeout, cancellation, or any mutation not explicitly expected from validation.
8. Stage exactly the cycle's approved paths with pathspec-safe commands; never use `git add .` or `git commit -a`.
9. Recheck the staged diff and clean unstaged state, then commit.
10. Use `babysit: fix required check <check-name>` as the v1 commit convention, sanitizing and truncating the check name. Add commit trailers `Babysit-Check:` and `Babysit-Cycle:` for auditability.
11. Push the current branch normally, read the resulting local SHA, record it as recognized, and verify the PR adopts that exact SHA.

If the agent makes no changes, reports ambiguity, or recommends no safe validation command for a substantive code fix, stop as not-fixable. Automatic mode requires `classification=code_fixable`, high classification confidence, high fix confidence, and a non-empty validation list.

Interactive mode asks once before fix mode and once before commit/push. The second prompt shows the diff, validations, proposed commit message, and any departure from the approved plan.

## Interruption and crash semantics

The process does not promise automatic resume, but every interrupted state must be reconcilable:

- before a commit, leave any verified workspace changes in place and report their paths;
- after a local commit but before push, report the commit SHA and do not retry automatically;
- after push starts but before remote/PR verification, report the local SHA and require the user to compare it with the PR head;
- never delete user-visible changes during cancellation cleanup;
- temporary diagnosis snapshots may be removed only when they contain no user changes.

A later invocation runs normal preflight and will reject residual changes rather than assuming ownership of them.

## Logs and rerun suggestions

Resolve the failed check to its check run and workflow run from structured metadata. Fetch logs through GitHub APIs or `gh run view <run-id> --log-failed` when the run identity is unambiguous. Preserve the check URL in every failure report.

If logs are expired, inaccessible, empty, or cannot be mapped confidently, stop without invoking fix mode. For Actions failures classified as flaky/infrastructure, print a non-executed suggestion such as `gh run rerun <run-id> --failed` only after verifying the run ID and repository. Never execute it in v1. For external checks, show the check URL without inventing a rerun command.

## Timeouts and polling

Defaults proposed for the first real-world exercise:

- poll interval: 15 seconds, with bounded jitter after rate-limit responses;
- required-suite appearance timeout: 10 minutes per recognized SHA;
- overall timeout: 6 hours;
- agent invocation: 30 minutes;
- each local validation command: 30 minutes unless a smaller adapter recommendation is supported later;
- automatic fix budget: 3 cycles that produced verified workspace changes. Invocation failures, malformed responses, ambiguity, and no-change outcomes are immediate safety/not-fixable stops and do not silently consume and retry the budget.

All waits use the command context and an injectable clock. Rate-limit responses honor GitHub reset/retry metadata but never extend the overall deadline silently.

## Reporting

Output is plain text and line-oriented so it remains useful in tmux and redirected logs. Each transition prints PR number/URL, abbreviated recorded SHA, cycle `n/3` in auto mode, and the relevant check. Final summaries include required and optional checks, diagnosis, validation commands/results, changed files, commits/pushed SHAs, and a suggested next action for non-success exits.

Terminal color and spinners are optional presentation only; correctness must not depend on a TTY.

## Test strategy

### Unit tests

- PR argument parsing and branch-based resolution.
- Exit-category to numeric-code mapping.
- Porcelain `-z` parsing and dirty-tree summaries.
- Check state normalization, app-qualified identities, duplicate attempts, authoritative suite tracking, historical hints, and two-poll stability.
- Every state-machine transition, cancellation, and timeout with fake clock/interfaces.
- Agent schema parsing and rejection of malformed/partial output.
- Repository-boundary mutation detection, validation-command policy, exact staging, commit-message sanitization, crash states, and cycle budget.
- Interactive accept/decline paths.

### Subprocess contract tests

Use temporary repositories and fake `gh`, `git`, and agent executables placed first on `PATH`. Assert complete argv, stdin, environment, cancellation, stdout/stderr limits, and exit handling. Tests must assert prohibited Git arguments never occur.

### GitHub integration tests

In a disposable repository, exercise:

- no required checks versus required checks not yet created;
- pending, success, failure, rerun, and optional failure;
- classic protection and ruleset-required checks;
- an external head update while polling;
- log retrieval and expired/unavailable logs.

These tests may be opt-in and credential-gated, but the release checklist requires them.

### Copilot spike acceptance

In a disposable branch with a synthetic failing log, demonstrate and record:

1. noninteractive execution and version detection;
2. diagnosis with zero workspace mutations;
3. extraction of one schema-valid outcome;
4. a separate authorized fix invocation that edits only expected files;
5. structured validation recommendations;
6. clear ambiguous and unable-to-fix outcomes;
7. inability to commit, push, or rewrite history;
8. deterministic timeout/cancellation behavior.

A prompt-only promise is insufficient for items 2 and 7. If permission flags cannot enforce them, use an OS/worktree wrapper or fail the spike and reassess the adapter.

## Delivery roadmap

### Milestone 0: Detemplate and establish contracts

- Rename module, binary, Cobra command, release config, README, and project instructions.
- Define exit codes, rich check identities, separate agent confidence fields, subprocess abstraction, and state-machine tests.
- Add architecture decision records for required-check discovery and agent permissions.

Exit criterion: `make ci` passes and the command exposes documented placeholder behavior without pretending to watch CI.

### Milestone 1A: Copilot adapter and isolation spike

- Build the versioned request/outcome schema and normalizer.
- Run the disposable-repository experiments above.
- Compare diagnosis and fix isolation models, including cost in a large repository.
- Document exact supported Copilot version range and invocation/permissions.
- Decide whether direct CLI invocation is sufficient or a wrapper is required.

Exit criterion: all eight spike acceptance items have reproducible evidence and the isolation model is selected. Do not start autonomous Git orchestration before this passes.

### Milestone 1B: GitHub required-check semantics spike

- Define the SHA-addressed provider and rich check identity contract.
- Compare `gh pr checks --required`, GraphQL, and REST behavior.
- Exercise classic branch protection, rulesets, app-qualified checks, commit statuses, reruns, dynamic suites, and zero-required-check configuration.
- Prove exact check-to-workflow-run/log mapping or document the safe unsupported cases.

Exit criterion: the provider can distinguish absent-yet checks from zero configured checks and return authoritative required identities for an exact SHA. This spike may proceed in parallel with Milestone 1A.

### Milestone 2: Read-only GitHub watcher

- Implement preflight, PR resolution, exact-SHA snapshots, the proven required-check provider, appearance guard, polling, logs, timeout, and final optional-check summary.

Exit criterion: acceptance criteria 1–5, 10–12, and required/optional semantics pass without any workspace mutation.

### Milestone 3: Interactive fix transaction

- Integrate diagnosis, consent boundaries, fix invocation, repository mutation checks, validation, exact staging, commit, normal push, and expected-SHA adoption.

Exit criterion: acceptance criteria 6, 7, 9, 13, and all Git safety invariants pass.

### Milestone 4: Automatic mode and hardening

- Require high-confidence code attribution, enforce three-cycle budget, improve actionable safety-stop summaries, and test cancellation/rate limits/large logs.

Exit criterion: acceptance criteria 8, 14, and 15 pass in automated tests and a real PR exercise.

### Milestone 5: Release readiness

- Complete user documentation, threat/safety review, stable exit-code documentation, changelog, cross-platform builds, and release preflight.
- Record the supported `gh` and Copilot CLI versions used in the real PR exercise.

## Decisions and release gates

Decisions made here:

- Go remains the implementation language.
- `gh`/`git` subprocesses are the initial integration boundary.
- Required checks come from GitHub/`gh` required-check semantics, never name-based local policy.
- Commit convention is `babysit: fix required check <name>` plus audit trailers.
- Defaults are 15-second polling, 10-minute suite appearance, and 6-hour overall timeout.
- Auto mode requires high classification and fix confidence and stops after three cycles that produce verified changes.

Must be proven before release:

1. `gh pr checks --required` correctly covers active rulesets and app-qualified checks, or it must be replaced with an exact-SHA API implementation.
2. The implementation can positively distinguish zero configured required checks from checks that have not appeared when configuration APIs are authorized; limited-token fallback must time out safely rather than infer zero from an empty rollup.
3. Copilot diagnosis can be made technically read-only.
4. Copilot fix mode can be prevented from committing, pushing, or rewriting history.
5. Log-to-check/run mapping is unambiguous for required GitHub Actions checks.
6. The selected diagnosis/fix isolation model is practical in large repositories.
7. Validation execution has a documented command and network policy with post-command mutation checks.
8. Minimum supported `gh` and Copilot versions and capability probes are documented.
