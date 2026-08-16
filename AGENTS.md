# carbonctl agent guide

carbonctl is a loopback-only Go bridge between an OpenCentauri (Elegoo
Centauri Carbon) 3D printer's SDCP websocket/camera/history endpoints and MCP
tool clients. It is the mechanism behind "talk to my printer." The shared,
always-on hermes-agent persona (nicknamed "neo") provides the conversation
layer, and carbonctl is one
of its toolsets (see `nix/modules/nixos/services/hermes-agent/default.nix`'s
`toolsets` list and `mcp-servers.nix`'s `carbonctl` block). carbonctl uses its
own dedicated Telegram bot for outbound printer alerts, not the shared
persona's bot. It does not receive Telegram updates.

There is no README in this directory; the package doc comments and the Nix
deployment modules below are canonical. Prefer reading those over asking.

## Start here

- `cmd/carbonctl/main.go` wires every collaborator together — read it first to
  see the full runtime shape (flags, printer client, lifecycle engine,
  control manager, HTTP server).
- Each internal package opens with a one-line doc comment stating its
  responsibility (`go doc ./internal/...` or just the top of each file).
- `module.nix` is the deployment contract: CLI flags, systemd hardening,
  secrets. It is also the source of truth for default ports/paths — don't
  hardcode them elsewhere.
- `nix/modules/nixos/services/hermes-agent/` is the conversational layer on
  top of this service. `settings.nix` owns the shared Hermes persona, and
  carbonctl's MCP allowlist lives in that same settings module.
- `tests.nix` is the NixOS integration test and doubles as a worked
  example of the full config surface. `nix/lib/checks.nix` collects it by
  name, so it runs as `checks.carbonctl` without any entry in `flake.nix`.

## Runtime boundaries

- Only `internal/control.Manager` may mutate the printer, and only through
  `printer.Commander.Send`, which allowlists SDCP commands 129/130/131
  (pause/stop/resume) and rejects everything else.
- Stop is two-phase by design: `PrepareStop` mints a task-bound token,
  `ConfirmStop` re-validates it and requires MCP elicitation approval
  (`internal/server/control.go`) before sending the command. Never add a
  one-shot stop path.
- `internal/state.Store` is the sole source of truth for current printer
  status and which milestones have already been notified; write through it
  rather than caching status elsewhere.
- The HTTP listener must stay loopback-only (`server.ErrNonLoopback`,
  enforced in `cmd/carbonctl/main.go`). The shared hermes-agent persona
  reaches it through the local MCP transport. Never widen this listener.
- `internal/lifecycle.Engine` is the only thing that sends proactive
  Telegram notifications (print start/progress/complete/pause/error/
  disconnect/recovery). It uses carbonctl's own dedicated bot for outbound
  delivery only. MCP tool calls in `internal/server` never notify directly.

## Modules

- `internal/printer`: SDCP websocket telemetry client and command sender —
  the only code that talks to the printer's mainboard.
- `internal/control`: pause/resume/stop policy and the stop-confirmation
  token lifecycle.
- `internal/lifecycle`: observed status transitions → deduplicated milestone
  events → snapshot capture → Telegram delivery.
- `internal/state`: atomic on-disk status/milestone persistence.
- `internal/camera`, `internal/snapshots`, `internal/history`: on-demand JPEG
  capture, retained snapshot archive, and OpenCentauri print-history lookups,
  exposed as MCP tools via `internal/server/media.go`.
- `internal/notifications`: Telegram delivery (retry, text vs photo).
- `internal/server`: the loopback HTTP surface — health/ready/identity plus
  both a legacy JSON-RPC `/mcp` and the official MCP transport
  (`server.go`, `mcp_transport.go`, `tools.go`, `control.go`, `media.go`).

## Commands and gates

From `bin/carbonctl/`: `gofmt`, `go test ./...`, `go vet ./...`. Nix changes
touching the service or the hermes-agent wiring should build the `carbonctl`
package and evaluate the `carbonctl` NixOS test above.
