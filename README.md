# gh-babysit

`gh babysit` is a GitHub CLI extension that will watch required checks for an existing pull request and safely delegate diagnosis and optional fixes to a coding agent.

The current milestone is deliberately non-mutating: it verifies the checkout and pull request, then watches the required checks for the recorded head SHA. It does **not** invoke an agent, edit files, rerun checks, commit, or push.

## Usage

```text
gh babysit [<pr-number-or-url>] [--auto]
```

The optional pull request may be a number or URL; when omitted, it is resolved from the current branch. `--auto` reserves the future automatic mode and does not permit mutations in this milestone. Run `gh babysit --help` for polling and timeout flags.

A normal invocation runs repository preflight and polls GitHub's active required-check configuration and exact-SHA check observations. It waits for configured checks to appear and requires two consecutive successful snapshots, so a newly pushed commit cannot pass while its suite is still absent. Optional checks are reported but do not gate success.

When a required check fails, `gh babysit` prints its URL and bounded failed-job logs when an Actions run can be identified. It does not suggest or execute a rerun until a later milestone can safely classify the failure. An unexpected PR-head change, ambiguous required-check configuration, unavailable GitHub data, or cancellation stops safely. Rate-limit reset metadata is honored within the overall deadline. `--appearance-timeout` bounds suite appearance; `--timeout` bounds the entire watch, including in-flight GitHub and log operations.

## Installation and development

```bash
gh extension install maxbeizer/gh-babysit
make ci
make build
make install-local
```

The approved v1 architecture, safety boundaries, exit codes, and roadmap are in [`docs/design.md`](docs/design.md).

## Exit codes

| Code | Meaning |
|---:|---|
| 0 | All required checks passed for the recorded head SHA |
| 2 | Usage, configuration, executable, or authentication error |
| 3 | User declined or canceled an interactive action |
| 4 | Failure is not confidently code-fixable |
| 5 | Safety stop or state conflict |
| 6 | Timeout |
| 130 | Interrupted by SIGINT |

## Releasing

`make release-check` verifies formatting and module tidiness, runs CI and vulnerability scanning, validates GoReleaser, and creates a local snapshot. Tag pushes matching `v*` run the release workflow; ordinary development must use `make ci`.
