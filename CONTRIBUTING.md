# Contributing to Towline

Thank you for your interest in contributing to Towline. This guide will help you
get started.

## Getting Started

1. Clone the repository:
   ```bash
   git clone https://github.com/your-org/towline.git
   cd towline
   ```
2. Install **Go 1.24+** (see https://go.dev/dl/).
3. Build and verify:
   ```bash
   make build
   make test
   ```

## Development Workflow

- Branch from `main` for every change.
- Write tests first (TDD) — add or update tests before writing implementation
  code.
- Before committing, run:
  ```bash
  make vet && make fmt
  ```
- Keep commits small and focused on a single concern.

## Project Structure

| Path | Purpose |
|------|---------|
| `cmd/towline-mcp/` | MCP server entry point |
| `cmd/towline/` | CLI entry point |
| `internal/middleware/` | Stack scoping, tier gating, container ownership |
| `internal/towline/` | Towline-specific MCP tool handlers |
| `internal/proxy/` | Traefik, Caddy, Cloudflare proxy backends |
| `internal/approval/` | In-chat approval token store |
| `internal/cli/` | CLI command implementations |
| `internal/mcp/` | Upstream Portainer MCP handlers (forked) |
| `pkg/config/` | Global and project config types |
| `pkg/portainer/` | Upstream Portainer client |
| `templates/` | Agent config and compose templates |
| `skills/` | DevOps skill definitions for agents |
| `tests/integration/` | E2E and regression tests |

## Testing

- **Unit tests:** `make test`
- **Integration / E2E tests:** `go test ./tests/integration/`
- The project currently has **207 tests**. All tests must pass before you submit
  a pull request.
- Add table-driven tests for any new logic.

## Upstream Fork Maintenance

The following packages are forked from **Portainer MCP v0.7.0**:

- `internal/mcp/`
- `internal/tooldef/`
- `pkg/portainer/`
- `pkg/toolgen/`

Only two files are modified from upstream:

- `internal/mcp/server_towline.go`
- `cmd/towline-mcp/main.go`

All new Towline code must go in **separate packages** (e.g., `internal/towline/`,
`internal/middleware/`). Do not modify the forked packages unless absolutely
necessary, so the fork remains cleanly mergeable with future upstream releases.

## Code Style

Follow the conventions already established in the codebase:

- **Exported** identifiers use `PascalCase`.
- **Unexported** identifiers use `camelCase`.
- Wrap errors with context: `fmt.Errorf("failed to do X: %w", err)`.
- Prefer **table-driven tests** with clear subtest names.
- Run `make vet && make fmt` to catch issues before committing.

## Submitting Changes

1. **Open an issue first** for large or architectural changes so we can discuss
   the approach before you invest significant effort.
2. Open a pull request against `main`.
3. Include tests that cover your changes.
4. Ensure all existing tests pass (`make test`).
5. Keep your PR focused — one logical change per PR.

## Questions?

Open a GitHub issue if anything is unclear. We are happy to help.
