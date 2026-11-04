# AGENTS.md

carbonctl is an MCP server that lets an assistant watch, pause, resume and stop a real 3D printer (Elegoo Centauri Carbon, OpenCentauri) and nothing else. Go, Nix flake. [`README.md`](README.md) has the tools and the safety design.

## Check

- `nix develop -c go test ./...` while working.
- `nix flake check` before pushing: it builds the package (which runs the tests) and a NixOS VM test of the module.
- `nix fmt -- *.nix` formats the Nix files (bare `nix fmt` reads stdin).

## Laws

- Only `internal/control.Manager` may change the printer, and only through
  `printer.Commander.Send`, which allowlists SDCP commands 129/130/131.
  Read-only queries (320/321, history) go through `printer.Querier`, a separate
  allowlist; never add a command to both, and never add a mutation to `Querier`.
- A command goes out only if `Manager` re-read the status just before sending
  and the same task is still printing or paused. Keep that recheck.
- `internal/state.Store` is the only source of truth for printer status. Write
  through it; don't cache status elsewhere.
- The HTTP listener stays loopback-only (`server.ErrNonLoopback` in
  `cmd/carbonctl/main.go`). Never widen it.
