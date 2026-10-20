# Changelog

All notable changes to this project will be documented in this file. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.6] - 2026-10-20

### Fixed

- Clean noisy GitHub Actions aggregate-check output while preserving structured errors, annotation-only failures, child-job links, and actionable workflow guidance.

## [0.1.5] - 2026-10-20

### Fixed

- Continue Codespaces required-check discovery when GitHub recomputes pull request merge state between paginated GraphQL responses, while withholding authoritative completion until every page is clean.

## [0.1.4] - 2026-10-20

### Fixed

- Replace raw failed-check log dumps with concise failure details, failed child-job links, workflow guidance, and a command for viewing full logs.
- Add `--show-logs` for explicitly printing the bounded raw failed-check log without duplicating final error output.

## [0.1.3] - 2026-10-20

### Fixed

- Recognize Codespaces configuration permission errors when GitHub CLI appends a trailing HTTP error summary, preserving the headers needed to select required-check fallback.

## [0.1.2] - 2026-10-20

### Fixed

- Watch required checks with the default GitHub Codespaces integration token by falling back to exact-head PR requiredness without allowing incomplete suites to report success.

## [0.1.1] - 2026-10-20

### Fixed

- Build release binaries without CGO so Linux installations do not depend on the release runner's glibc version.

## [0.1.0] - 2026-10-20

### Added

- Read-only required-check watching for an existing pull request and exact head SHA.
- Pull request and checkout safety preflight with dirty-tree, branch, remote, permission, and concurrent-change detection.
- Required-check discovery across branch protection and repository rulesets, with optional-check reporting.
- Bounded failed-check logs, stable-success detection, timeout handling, and conservative rerun suggestions.
- Copilot CLI adapter contract and isolation spike for future diagnosis and fix workflows.
- Typed exit codes, core domain contracts, subprocess and clock seams, and capability checks.

[Unreleased]: https://github.com/maxbeizer/gh-babysit/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/maxbeizer/gh-babysit/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/maxbeizer/gh-babysit/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/maxbeizer/gh-babysit/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/maxbeizer/gh-babysit/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/maxbeizer/gh-babysit/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/maxbeizer/gh-babysit/releases/tag/v0.1.1
[0.1.0]: https://github.com/maxbeizer/gh-babysit/releases/tag/v0.1.0
