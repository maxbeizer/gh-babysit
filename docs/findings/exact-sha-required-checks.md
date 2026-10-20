# Exact-SHA required-check discovery spike

Date: 2026-10-19

## Decision

The watcher boundary is `github.Provider.Discover(Request)`, where `Request` contains the owner, repository, base branch, and immutable head SHA. The selected implementation combines read-only REST configuration endpoints with exact-SHA REST observations:

1. `GET /repos/{owner}/{repo}/branches/{branch}/protection/required_status_checks`
2. `GET /repos/{owner}/{repo}/rules/branches/{branch}`
3. `GET /repos/{owner}/{repo}/commits/{sha}/check-runs`
4. `GET /repos/{owner}/{repo}/commits/{sha}/statuses`
5. `GET /repos/{owner}/{repo}/actions/runs/{run_id}` when an Actions details URL provides an unambiguous run ID

A successful result marks configured requirements authoritative. Classic protection's specific “Branch not protected” 404 means no classic requirement. **Any** failure reading applicable branch rules, including 404, fails discovery. It is not converted to an empty ruleset. This is how the boundary distinguishes a proven zero configured requirements from configured checks that have not appeared on the SHA.

The provider returns configured identities separately from observations. Therefore an empty observation list cannot imply success when requirements are non-empty. The later watcher must still impose the design's appearance timeout and two-consecutive-poll success rule.

## Codespaces integration-token fallback

Codespaces commonly authenticates `gh` with an integration token that can read PRs and checks but receives `403 Resource not accessible by integration` and `X-Accepted-GitHub-Permissions: administration=read` from configuration endpoints. For that exact denial, the provider falls back to a paginated GraphQL query over the recorded commit's `statusCheckRollup`, using `CheckRun.isRequired(pullRequestNumber:)` and `StatusContext.isRequired(pullRequestNumber:)`. The query also returns the commit OID; a different or missing OID is rejected. The watcher additionally brackets discovery with PR-head reads.

GraphQL requiredness is joined with the existing exact-SHA REST observations, which continue to provide App IDs, object IDs, attempts, workflow-run mappings, and log/rerun metadata. An authenticated query against PR 14 confirmed that GitHub accepts both `isRequired` fields and exposes a check run's app through `checkSuite.app.databaseId`.

The fallback is intentionally narrow. It is selected only when the response body reports `Resource not accessible by integration` and the parsed `X-Accepted-GitHub-Permissions` response header includes `administration=read`; it is not selected for 401 responses, rate limits, generic 403 responses, or body text that merely resembles the header. Most importantly, an empty, optional-only, or partially populated rollup does **not** prove the complete configured set: another required check may not have been created yet. Fallback completion additionally requires GitHub's positive `PullRequest.mergeStateStatus=CLEAN` signal tied to the recorded head. Otherwise the watcher continues pending checks or stops safely with actionable Administration-read guidance. Authorized REST configuration discovery remains the only implemented path that directly establishes zero required checks.

## Identity and normalization

A configured identity is `(context, app ID?)`. App-qualified and unqualified requirements are intentionally distinct. Classic `checks[].app_id` and ruleset `required_status_checks[].integration_id` supply the qualifier; commit statuses remain unqualified because their creator user ID is not a GitHub App ID.

Observations retain source kind, check/status ID, URL, raw state, normalized state, app ID, timestamps, workflow run ID, and attempt when available. Check runs and statuses are paginated. Duplicate observations collapse by rich identity and source, preferring workflow attempt, then completion/start time, then object ID. Normalization is conservative:

- queued/in-progress check runs and pending statuses -> `pending`
- successful values -> `success`
- failure, error, cancelled, timed out, and action-required -> `failure`
- neutral, skipped, and stale remain distinct terminal states

An Actions workflow run ID is accepted only from the documented Actions details-URL shape (`/actions/runs/{id}/...`). Its `run_attempt` is then read from the workflow-run REST resource. External URLs are not guessed. If Actions run enrichment fails, discovery fails rather than returning misleading rerun/log metadata.

## API comparison

### `gh pr checks --required`

Advantages:

- delegates requiredness semantics to GitHub/`gh`;
- convenient buckets and display fields.

Blocking limitations:

- PR-oriented, not SHA-addressed; it requires before/after PR-head bracketing;
- JSON does not expose App IDs, check/status IDs, source kind, workflow run IDs, or attempts;
- an empty result alone does not prove “no configured requirements”;
- it cannot run without an actual PR. This repository had no PRs during the exercise; `gh pr checks 4` correctly failed because 4 is an Issue, not a Pull Request.

It is not the production provider.

### GraphQL

`Commit.statusCheckRollup.contexts` gives one exact-SHA union of `CheckRun` and `StatusContext`. In authenticated exercises it exposed check-run requiredness for a PR, the check-run database ID, nested workflow-run ID, and the GitHub App database ID through `CheckRun.checkSuite.app.databaseId` (there is no direct `CheckRun.app` field). It does not independently enumerate configured requirements that have not appeared, so `PullRequest.mergeStateStatus=CLEAN` is required as the positive completion signal; the rollup alone cannot prove either an empty or complete configuration.

### REST

REST supplies check-run `app.id`, separate commit statuses, object IDs, and the authoritative configuration inputs. The check-run details URL maps GitHub Actions jobs to run IDs; the workflow-run resource supplies `run_attempt`. REST requires joining and careful error semantics, but it is the only evaluated strategy that can represent the required boundary without weakening identity.

## Authenticated evidence from this repository

Against private repository `maxbeizer/gh-babysit`, using authenticated `gh` read-only calls:

- remote `main` was `096e4453e83250dabdcafb54e447c95f500bdb67`;
- classic required-status protection returned “Branch not protected” (HTTP 404);
- applicable branch rules returned `[]`;
- exact-SHA REST returned one successful Actions check run, ID `118272759629`, app ID `15368`, details URL containing workflow run `39314621853`, and no commit statuses;
- GraphQL returned the same check ID and nested workflow run ID;
- the repository had no pull requests, so `gh pr checks --required` could not be exercised on a PR.

These observations prove the no-required-configuration path and REST/GraphQL exact-SHA observation mapping in this repository. They do not prove every GitHub product/configuration combination below.

## Fixtures and covered behavior

Checked-in API fixtures and unit tests cover:

- classic and ruleset requirements, including app-qualified checks;
- a legacy commit-status requirement;
- required checks not yet observed versus authoritative zero requirements;
- optional checks;
- check-run and commit-status state normalization;
- reruns collapsing to the latest attempt;
- Actions run mapping and conservative rejection of external URLs;
- fail-safe ruleset API errors.

The ruleset and rerun fixtures are contract fixtures, not claims that those configurations were created in this repository.

## Unproven and release-gated cases

This repository cannot prove repository-ruleset API behavior, app-qualified enforcement, dynamic suites/matrices, fork PRs, merge queue behavior, or a real rerun because it has no applicable rulesets/protection or PRs and this spike was forbidden from mutating GitHub. In particular, GitHub's applicable-rules endpoint is treated as authoritative according to its API contract, but interaction among organization rulesets, bypass state, evaluate mode, and token visibility was not empirically demonstrated here.

The implementation therefore fails safe whenever applicable rules cannot be read. It does not approximate rulesets from observed names, prior SHAs, repository ruleset listings, or `gh pr checks`. Before the watcher may claim support for ruleset-protected repositories, run the credential-gated integration matrix described in `docs/design.md` in a disposable repository and record results for classic protection, repository and organization rulesets, app qualification, commit statuses, reruns, dynamic suites, optional checks, forks, and merge queue. Until then, a ruleset endpoint error is a safety stop, not success.
