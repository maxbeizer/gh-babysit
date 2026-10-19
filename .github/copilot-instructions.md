# Copilot instructions for gh-babysit

`gh-babysit` is a Go GitHub CLI extension. Its approved architecture and safety boundaries are in `docs/design.md`.

- Keep `main.go` limited to command wiring, signals, and exit-code translation.
- Organize implementation by functionality under `internal/`; keep interfaces narrow and consumer-owned.
- Use structured `gh` output and direct subprocess argument arrays. Never add an implicit shell fallback.
- Preserve the non-mutating safety boundary for the current milestone. Do not implement watching, agent invocation, commits, pushes, reruns, or other GitHub mutations ahead of their roadmap milestones.
- Preserve `gh babysit --help` and `gh babysit --version`.
- Add table-driven tests for command parsing, state transitions, exit mapping, subprocess contracts, and new safety behavior.
- Run `gofmt`, `go mod tidy`, and `make ci` before committing.
