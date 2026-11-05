---
name: centauri-carbon
description: Centauri Carbon is an Elegoo printer running OpenCentauri. Use when status shows printing, paused, or stopping, or the camera is the chamber.
license: MIT
compatibility: Elegoo Centauri Carbon on OpenCentauri. Act through carbonctl.
metadata:
  author: kuhree
---

# Centauri Carbon

Control this printer through carbonctl. Load that skill to reach the server. Do not open the printer's WebSocket, discovery port, or camera URL.

## Read the state

Trust `state` and `connected`. A filename, progress, or layer count can remain after the print ends. An idle printer is not printing.

| state | meaning | control |
|---|---|---|
| printing | a job is running | pause, or stop after the owner confirms |
| paused | the job is held | resume, or stop after the owner confirms |
| stopping | stop was accepted, not finished | wait, then read status |
| stopped, complete, idle | no active job | do not send pause, resume, or stop |
| error | the printer reported a fault | do not send a control command; report `error_number` |
| file-transfer, calibrating, testing | busy, not a pauseable print | do not send a control command |
| unknown | the status did not map | do not send a control command |

`connected: false` means those numbers are the last observation. Do not act on them.

Report the temperatures in the status. A 0 can mean the printer did not report that reading. Do not treat a target as the current temperature.

## Act

Pause holds the job. Resume continues it. Stop ends it and cannot be undone.

Before stop, load `carbonctl` and follow its stop step. Read status again and report the state you see, including `stopping`.

## Camera and history

Take one snapshot through carbonctl. It grabs one frame and closes the stream. Do not hold the camera URL open. A held stream can lock other viewers out.

History is a separate query, not the live status. `availability: unavailable` means the query failed, not that the printer has never printed. A history result (`completed`, `exceptional`, `stopped`, `other`) is about a past job.

## Guardrails

Heat, home, move, upload, and delete are not in carbonctl. Say so. Do not do them by another path.
