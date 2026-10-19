# gh-babysit

`gh babysit` is a GitHub CLI extension that will watch required checks for an existing pull request and safely delegate diagnosis and optional fixes to a coding agent.

The current milestone is deliberately non-mutating: it establishes the command surface and internal contracts, but does **not** watch CI, invoke an agent, edit files, commit, or push.

## Usage

```text
gh babysit [<pr-number-or-url>] [--auto]
```

The optional pull request may be a number or URL. `--auto` reserves the future automatic mode. Run `gh babysit --help` for timeout and agent-adapter flags. Until the read-only watcher milestone is implemented, a normal invocation reports that watching is unavailable and exits without changing anything.

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
| 0 | Success |
| 2 | Usage, configuration, executable, or authentication error |
| 3 | User declined or canceled an interactive action |
| 4 | Failure is not confidently code-fixable |
| 5 | Safety stop or state conflict |
| 6 | Timeout |
| 130 | Interrupted by SIGINT |

## Releasing

`make release-check` verifies formatting and module tidiness, runs CI and vulnerability scanning, validates GoReleaser, and creates a local snapshot. Tag pushes matching `v*` run the release workflow; ordinary development must use `make ci`.
