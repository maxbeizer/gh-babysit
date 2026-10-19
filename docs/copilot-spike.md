# Copilot CLI adapter spike

Status: implemented and exercised on 2026-10-19. This records the reproducible evidence for issue 3; it is not a general promise about future Copilot releases.

## Supported contract and invocation

The adapter owns schema version 1 in `internal/agent`. Requests include repository/PR/check identity, log paths, mode, prior diagnosis, current diff, and constraints. Outcomes require every documented field, including independent classification and fix confidence. The normalizer accepts exactly one JSON object, rejects unknown/missing fields and enum values, and treats validation commands as argv arrays rather than shell text.

Supported Copilot CLI range is `>=1.0.96, <2.0.0`; startup also checks recognizable version output. The exercised binary was:

```text
GitHub Copilot CLI 1.0.96-2.
```

Both modes use this base invocation (the prompt is passed as one argv value):

```text
copilot -C <workspace> -p <prompt> --silent --stream off \
  --no-ask-user --no-auto-update --no-custom-instructions \
  --disable-builtin-mcps --disallow-temp-dir --output-format text \
  --excluded-tools=shell
```

Diagnosis adds `--deny-tool=write`. Fix adds `--allow-tool=write`. It deliberately does **not** use `--allow-all-tools`, `--allow-all-paths`, or URL permissions. The process environment removes Git redirection variables and `COPILOT_ALLOW_ALL`, disables auto-update and tgrep, and preserves the remaining user environment needed for CLI authentication.

The shell tool is absent in fix mode, so the agent cannot invoke `git` (directly or through another shell), `gh`, build commands, commit, push, reset, rebase, or hooks. Validation is only recommended as structured argv and will be run later by the orchestrator's separate policy boundary. Built-in file editing remains available. This is stronger and less bypass-prone than command-name deny patterns.

## Isolation decision

Diagnosis runs in a temporary detached Git worktree at the recorded SHA. It has no write tool and no shell tool. The adapter requires the real checkout to be clean, records its HEAD and complete porcelain status before the run, destroys/prunes the temporary worktree afterward, and requires the real checkout identity to be unchanged. Thus a Copilot permission regression that enables a built-in write affects only the disposable checkout; the postcondition independently detects real-checkout changes.

Fix runs in the real checkout with built-in reads/edits but no shell. This is the smallest viable v1 because the orchestrator must inspect and validate the resulting diff before any later Git mutation. A detached fix worktree plus patch transfer would improve attribution but adds symlink, binary patch, mode, submodule, and cancellation semantics that are not needed to prove this adapter boundary.

Isolation alternatives considered:

| Model | Finding |
|---|---|
| Full filesystem copy | Strong separation but copies ignored/untracked data and scales with all bytes; preserving links/modes safely is additional code. |
| Detached Git worktree | Selected for diagnosis. Materializes tracked files, shares objects, pins the recorded SHA, and has standard Git cleanup. It briefly changes worktree metadata in the common Git directory, but not HEAD/index/files of the real checkout. |
| Read-only OS mount | Potentially strongest for the real path, but not portable or normally available unprivileged on macOS, Linux, and Windows. Copilot state/index behavior also differs by host. |
| Copilot permissions only | Useful defense in depth, but documented as best-effort for built-in writes and not sufficient alone for diagnosis. Shell permissions without OS sandboxing do not confine absolute paths. |
| Copilot MXC sandbox | Promising, but disabled by default, host prerequisites differ, built-in edits remain best-effort, and the current CLI has no stable per-invocation policy argument sufficient for this adapter. Not selected as the primary boundary. |

A synthetic repository with 10,000 tracked 1 KiB files was measured on the development macOS host. Detached worktree add/remove took 3.99s/2.30s; `cp -R` took 12.33s. This is not a universal benchmark, but confirms the worktree approach avoids copying Git objects and is practical enough to test in a larger real repository before release. Reproduce by generating the files, committing them, then timing `git worktree add --detach`, `git worktree remove --force`, and `cp -R` as shown in the issue implementation session.

## Acceptance evidence

1. **Noninteractive/version:** `TestProbe` covers accepted, old, future-major, and malformed versions. A real `-p --silent --stream off --no-ask-user` run exited without interaction on 1.0.96-2.
2. **Read-only diagnosis:** a disposable repository run with `--excluded-tools=shell --deny-tool=write` returned a diagnosis and left `git status --porcelain=v1` unchanged. `TestDiagnosisUsesDetachedWorktreeAndVerifiesRealWorkspace` proves the additional worktree and real-checkout postcondition.
3. **Strict outcome:** `TestNormalize` accepts one v1 object; table tests reject prose, a second value, unknown/missing fields, invalid enums/version, and malformed commands.
4. **Separate fix:** a disposable run with `--excluded-tools=shell --allow-tool=write` changed `fixture.txt` from `broken` to `fixed`; the original commit remained HEAD.
5. **Structured validation:** prompt and normalizer tests preserve `[["go","test","./..."]]` as argv, with no shell parsing.
6. **Ambiguous/unable outcomes:** the real diagnosis returned `ambiguous` with high classification confidence and low fix confidence. An earlier deliberately over-filtered experiment returned `fix_completed:false` and an inability summary, demonstrating honest no-tool behavior; normalizer tests cover the enum contract.
7. **No Git mutation:** in the fix experiment Copilot reported commit, push, and reset blocked because command execution was unavailable. HEAD was unchanged while only the expected file was modified. The subprocess-contract test asserts shell and all-permission flags are absent.
8. **Timeout/cancellation:** `TestTimeoutIsDeterministic` blocks the fake subprocess until context expiry and verifies a wrapped `context.DeadlineExceeded`. `exec.CommandContext` terminates the real child on cancellation; callers can distinguish parent cancellation from adapter timeout through the wrapped cause.

All real experiments used newly created temporary repositories with no remotes, and removed them afterward.

## Release gates and limitations

The spike supports a testable v1 adapter, but autonomous Git orchestration remains gated by the rest of `docs/design.md`:

- Re-run the isolation benchmark and real Copilot exercise on each supported OS and at least one genuinely large repository. The synthetic macOS result is encouraging, not a cross-platform guarantee.
- Confirm Copilot's tool names and exclusion semantics with a capability experiment when raising the supported version range; a version check alone is not sufficient.
- The caller must place capped/truncated check logs inside the isolated workspace (or copy them there safely) before invocation. Log acquisition and cap enforcement belong to the later GitHub/orchestration milestone.
- Fix postconditions (HEAD/index unchanged, expected path set, metadata/submodule checks) remain the orchestrator's responsibility before validation or staging.
- `exec.CommandContext` provides deterministic cancellation but cannot guarantee graceful descendant cleanup for every platform/process tree. Add platform integration coverage before release.
- No autonomous commit, push, history rewrite, validation execution, watcher integration, or GitHub mutation is implemented by this spike.
