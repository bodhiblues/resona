---
name: verify
description: Build and drive Resona (Go/Bubble Tea TUI music player) to verify changes end-to-end in a real terminal.
---

# Verifying Resona

Resona is a single-package Go TUI (Bubble Tea v2). Its surface is the
terminal; drive it under an isolated tmux server.

## Build & launch

```bash
go build -o resona-test .
tmux -L resona-verify new-session -d -x 180 -y 45 './resona-test'
sleep 3   # library (~2400 songs) loads at startup
tmux -L resona-verify capture-pane -p
```

Clean up: `tmux -L resona-verify kill-server; rm resona-test`.

## Driving it

- `/` opens fuzzy search; type a query, `Enter` plays the first result.
  Playing a FLAC from cold /data-pool storage can block the UI ~6s
  before the now-playing bar appears — wait before capturing.
- `Space` toggles pause. `s` stops. `f` cycles Library → Files → Radio.
- Radio tab opens a quick-play form prefilled with a somafm URL; `p`
  plays it without saving (needs network, emits audio).
- Status line (last pane row) shows `▶ Playing / ⏸ Paused / ⏹ Stopped`.

## Gotchas

- `pgrep -x resona-test` for the app PID — plain pgrep matches the tmux
  server too. Sample CPU: `top -b -n 3 -d 2 -p $PID`.
- The user often has a real `./resona` instance running — never kill it;
  both share `~/.config` state and `/tmp/resona_debug.log` (the app logs
  every playback attempt there; useful to confirm decode/play succeeded).
- Avoid `a` (add folder) / `r` (rescan) / `s` in radio forms — they
  mutate the user's real library/station config.
- Expected idle CPU after the 2026-07 fixes: ~1% stopped or paused,
  ~20% playing (20Hz render + visualizer). Playback emits real audio
  through PipeWire — keep it brief.
