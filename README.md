# gh-extension-template

A template for building [GitHub CLI](https://cli.github.com/) extensions in Go.

## Using this template

1. Create a new repo from this template:
   ```bash
   gh repo create my-org/gh-my-extension --template maxbeizer/gh-extension-template --private --clone
   cd gh-my-extension
   ```

2. Customize the generated repository:
   - **`go.mod`** — change the module path to your repository.
   - **`Makefile`** — change `EXTENSION_NAME` to the extension name without the `gh-` prefix.
   - **`.goreleaser.yml`** — change `project_name` and `binary` to `gh-<your-name>`, and confirm the `main.version` ldflag still matches your version variable.
   - **`main.go` and `main_test.go`** — change the command name, description, placeholder output, and expected version output, then implement your commands while retaining `--version` support.
   - **`README.md`** and the GitHub repository description — describe the extension and its installation and usage.
   - **`CHANGELOG.md`** — replace the comparison-link placeholders and maintain release entries.
   - **`.github/copilot-instructions.md`** — replace template-specific names and adjust the guidance to the project.
   - **`CODE_OF_CONDUCT.md`** — provide a private conduct-reporting channel owned by the project maintainers.

3. Verify the starter behavior:
   ```bash
   make ci
   make install-local
   gh my-extension
   gh my-extension --version
   ```

## Development

```bash
make help          # see all targets
make build         # build binary
make test          # run tests
make ci            # build + vet + test-race
make release-check # run the safe release preflight
make install-local # install extension from checkout
make relink-local  # reinstall after changes
```

`make release-check` checks formatting and module tidiness without retaining
changes, runs CI and vulnerability scanning, validates the GoReleaser config,
and creates a clean local snapshot in `dist/`. It does not tag, publish, or
create a GitHub release. Install a current GoReleaser v2 binary before running
it.

The starter command prints a placeholder greeting. Cobra provides `--help`,
and `--version` prints `dev` for ordinary local builds or the release tag for
GoReleaser builds.

## Releasing

Update the changelog, commit all intended changes, and run the preflight before
tagging:

```bash
make release-check
git status --short
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

The tag push triggers GitHub Actions and GoReleaser. It creates standalone
binaries plus checksums for all six supported targets:

- macOS (`darwin/amd64`, `darwin/arm64`)
- Linux (`linux/amd64`, `linux/arm64`)
- Windows (`windows/amd64`, `windows/arm64`)

Once released, users install with:

```bash
gh extension install my-org/gh-my-extension
```

## What's included

| File | Purpose |
|------|---------|
| `Makefile` | Build, test, lint, install, and release-preflight targets |
| `.goreleaser.yml` | Cross-platform standalone binary releases |
| `.github/workflows/release.yml` | Validated automated releases on tag push |
| `.github/workflows/ci.yml` | CI on pushes and pull requests to `main` |
| `main.go` | Minimal Cobra starter with version and signal handling |
| `.gitignore` | Go, editor, and OS ignores |
