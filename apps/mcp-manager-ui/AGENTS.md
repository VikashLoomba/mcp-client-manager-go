# AGENTS.md — mcp-manager-ui

Scope: Applies to everything under `apps/mcp-manager-ui/`.

## Module & Imports
- Go module path: `github.com/vikashloomba/mcp-client-manager-go/apps/mcp-manager-ui`.
- Keep imports to the root module using that repo path (e.g., `github.com/vikashloomba/mcp-client-manager-go/pkg/...`).
- CI builds this module with `GOWORK=off`. Avoid `replace` directives pointing to `../..` in this submodule when releasing.

## Versioning & Releases
- Tags for this submodule are path‑scoped: `apps/mcp-manager-ui/vX.Y.Z`.
- The Release UI workflow auto‑bumps version based on commit messages that TOUCH THIS DIRECTORY ONLY:
  - Include `#major` anywhere in the commit message → bumps MAJOR.
  - Include `#minor` anywhere in the commit message → bumps MINOR.
  - Otherwise → bumps PATCH.
- First release defaults to `apps/mcp-manager-ui/v0.1.0`.
- For MAJOR ≥ v2, also update `go.mod` module path to end with `/v2` (Go module rule), and the tag will be `apps/mcp-manager-ui/v2.0.0`.

Examples:
- `feat(ui): add server list #minor`
- `refactor: rename types` (no marker → patch)
- `feat!: change API shape (#major)`

Manual releases:
- You can trigger a manual run via GitHub Actions → “Release UI”. It will still skip if no changes in this path.

## Local Development
- Dev server: `wails3 dev -config ./build/config.yml`.
- Production build (per‑OS tasks): `wails3 task darwin:package:universal`, `wails3 task linux:package`, `wails3 task windows:package`.
- Regenerate bindings: `wails3 generate bindings -clean=true -ts` (also run via `task common:generate:bindings`).

## Wails v3 CLI
- This UI is a Wails v3 project; use the `wails3` CLI shipped with Wails v3.
- Core commands: `docs`, `init`, `build`, `dev`, `package`, `doctor`, `releasenotes`, `task`, `generate`, `update`, `service`, `tool`, `version`, `sponsor`.
- Global flag: `-help` (per-command help is also supported).
- `wails3 docs` opens the online guide: https://v3.wails.io/getting-started/your-first-app/.
- Run `wails3 doctor` for environment diagnostics and `wails3 update` to pull toolchain updates when needed.

## CI Artifacts (summary)
- Linux: `mcp-manager-ui_<tag>_linux_<arch>.tar.gz` (raw binary inside).
- macOS: `mcp-manager-ui_<tag>_macOS_universal.zip` (signed local dev cert; adjust signing if needed).
- Windows: zipped `.exe` and NSIS installer if present.

## Do/Don’t
- Do keep changes scoped to this folder when intending a UI‑only release.
- Don’t add cross‑module import cycles (the root must not import this module).
- Don’t leave temporary `replace` directives in `go.mod` when tagging.
