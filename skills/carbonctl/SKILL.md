---
name: carbonctl
description: carbonctl is the loopback MCP server for an OpenCentauri printer. Use when tools are named carbonctl_* or the process is carbonctl.
license: MIT
compatibility: A running carbonctl on loopback, pointed at one printer.
metadata:
  author: kuhree
---

# carbonctl

carbonctl lets an agent watch one printer and pause, resume, or stop the active print. It rejects every other command.

## Reach

The server listens on loopback only, usually `127.0.0.1:9086`. MCP is at `/mcp`. `GET /health` is the process. `GET /ready` is the printer: `ready` means connected, and the last observation is fresh.

Flags and the NixOS module are in `carbonctl --help` and the repo README. Read those. Do not invent a listen address, a printer URL, or a flag.

List tools from the live server. Call only a name that list returns.

## Act

1. Read status. Act only when `connected` is true. A false `connected` is the last observation, not a live printer.
2. Pause only while `state` is `printing`. Resume only while `state` is `paused`. Stop only while `state` is `printing` or `paused`, and only for the `task_id` just read.
3. Stop ends the print. It is one call, `carbonctl_stop`, with no arguments. Tell the owner the filename, progress, and state, and wait for them before sending it.
4. Control tools take no arguments. An extra field is an error and does not change the printer.

The server re-reads status and sends only if that same task is still in the right state. The printer's protocol carries no task id, so a print that ends or starts in the gap can still receive the command.

## Verify

Read status again after a control call. Report the state you see.

`outcome: confirmed` means the printer moved. `accepted-unconfirmed` means the command was sent and the printer had not confirmed within the wait. Read status again either way. Do not send stop a second time to make sure.

A snapshot or history result with `availability: unavailable` is a miss, not a crash. Say so.

## Guardrails

Keep the listen address on loopback. A non-loopback address exits with an error. Do not work around it.

If the tool list has no command for it, say so and stop. Do not open the printer's socket. Load `centauri-carbon` to read the printer. This server is how to act.
