# carbonctl

An MCP server for an Elegoo Centauri Carbon running
[OpenCentauri](https://github.com/OpenCentauri): print status, the camera,
print history, and pause, resume, and stop, for any MCP client on the same
machine.

```sh
nix run git+https://git.kuhree.com/kuhree.com/carbonctl -- \
  --printer-url ws://printer:3030/websocket \
  --camera-url http://printer:3031/video \
  --history-url http://printer/api/v1/print-tasks \
  --state ./state.json --snapshot-dir ./snapshots
```

On NixOS, `nixosModules.default` runs it as a sandboxed service that reaches
only the printer and its local clients:

```nix
services.carbonctl = {
  enable = true;
  printer = "192.168.1.50";
};
```

## Develop

`nix develop`, then `go test ./...`. `nix flake check` builds the package
(which runs the tests) and the module's VM test.

## Invariants

- Only `internal/control.Manager` may mutate the printer, and only through
  `printer.Commander.Send`, which allowlists SDCP commands 129/130/131
  (pause/stop/resume) and rejects everything else.
- Stop is two-phase: `PrepareStop` mints a task-bound token; `ConfirmStop`
  re-validates it and requires MCP elicitation approval
  (`internal/server/control.go`). Never add a one-shot stop path.
- `internal/state.Store` is the sole source of truth for printer status.
  Write through it; do not cache status elsewhere.
- The HTTP listener stays loopback-only (`server.ErrNonLoopback` in
  `cmd/carbonctl/main.go`). Never widen it.
