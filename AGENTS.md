# carbonctl agent guide

Loopback-only MCP bridge for OpenCentauri (Elegoo Centauri Carbon). Hermes
("neo") is the conversation layer; see
[OpenCentauri CC1 capabilities](https://git.kuhree.com/kuhree.com/fleet/wiki/OpenCentauri-CC1-Capabilities).

Canonical sources: `cmd/carbonctl/main.go`, package doc comments (`go doc
./internal/...`), `nix/modules/services/carbonctl/default.nix`, and
`nix/modules/services/hermes-agent/default.nix` (Fleet toolset wiring). Shared
base MCP settings come from the external `hermes-config` flake input. Verify
with `go test ./...` from `bin/carbonctl/` and
`nix build .#checks.x86_64-linux.carbonctl` for Nix changes.

## Invariants

- Only `internal/control.Manager` may mutate the printer, and only through
  `printer.Commander.Send`, which allowlists SDCP commands 129/130/131
  (pause/stop/resume) and rejects everything else.
- Stop is two-phase: `PrepareStop` mints a task-bound token; `ConfirmStop`
  re-validates it and requires MCP elicitation approval
  (`internal/server/control.go`). Never add a one-shot stop path.
- `internal/state.Store` is the sole source of truth for printer status and
  notified milestones. Write through it; do not cache status elsewhere.
- The HTTP listener stays loopback-only (`server.ErrNonLoopback` in
  `cmd/carbonctl/main.go`). Hermes reaches it through local MCP transport.
  Never widen this listener.
- Only `internal/lifecycle.Engine` sends proactive Telegram notifications, via
  carbonctl's dedicated bot. MCP tool calls never notify directly.
