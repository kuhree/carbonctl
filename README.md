# carbonctl

An MCP server for the Elegoo Centauri Carbon 3D printer (running
[OpenCentauri](https://github.com/OpenCentauri)). It lets an AI assistant check
on a print, look through the camera, read print history, and pause, resume or
stop a print, without being able to do anything else to the printer.

```mermaid
flowchart LR
  A[MCP client<br/>Claude, agents] -->|loopback HTTP| C[carbonctl]
  C -->|WebSocket, SDCP: status, control, history| P[Printer]
  C -->|MJPEG| K[Camera]
```

## Skills

```sh
npx skills add kuhree/carbonctl
```

`carbonctl` is this server. `centauri-carbon` is the printer: states, the
camera, and when a pause is not a stop. Claude Code can install the same pair:

```sh
claude plugin marketplace add kuhree/carbonctl
claude plugin install carbonctl@kuhree-carbonctl
```

## Tools

| Tool | What it does |
|---|---|
| `carbonctl_get_status` | Current printer state |
| `carbonctl_get_snapshot` | One fresh camera image |
| `carbonctl_get_history` | Recent print history |
| `carbonctl_pause` / `carbonctl_resume` | Pause or resume the active print |
| `carbonctl_stop` | Stop the active print |

## Safety by design

An assistant that can control real hardware needs hard limits:

- **Allowlist, not denylist.** Only pause, stop and resume (SDCP 129/130/131) can
  change the printer. Every other command is rejected in one place. History is
  read over a separate read-only allowlist (SDCP 320/321).
- **Loopback only.** The server refuses to listen anywhere but localhost.
- **One source of truth** for printer state, so the assistant never acts on a stale view.
- **Checked right before sending.** Pause, resume and stop re-read the status and
  send only if the same task is still in the right state. SDCP carries no task
  id, so a print that ends or starts in the moment between that check and the
  write can still receive the command.

## Run

```sh
nix run github:kuhree/carbonctl -- \
  --printer-url ws://printer:3030/websocket \
  --camera-url http://printer:3031/video \
  --state ./state.json --snapshot-dir ./snapshots
```

On NixOS, `nixosModules.default` runs it as a sandboxed service that can reach
only the printer and local clients:

```nix
services.carbonctl = {
  enable = true;
  printer = "192.168.1.50";
};
```

## Develop

`nix develop`, then `go test ./...`. `nix flake check` builds the package (which
runs the tests) and a NixOS VM test of the module.

Contributing rules are in [AGENTS.md](AGENTS.md).
