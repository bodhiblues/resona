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

## Mouse events

The app has mouse support; synthesize clicks with raw SGR sequences, e.g.
click col 134 / row 39 (1-based): `tmux -L resona-verify send-keys -t 0 -H
1b 5b 3c 30 3b 31 33 34 3b 33 39 4d` (press, `...6d` = release). Useful for
seeking on the progress bar.

## Gotchas

- After verification passes, ALWAYS rebuild the real binary the user
  launches (`go build -o resona .`) — verifying a throwaway build and
  leaving `./resona` stale caused a false "still broken" report once.
- In the radio tab, `s` means *save & play* in the add/quickadd views and
  *stop* everywhere else — stopping playback while a radio form is open
  saves a junk station to `~/.resona/radio_stations.json`. Stop from the
  library/files tab instead. The quickadd form only appears when the saved
  station list is empty.
- `/` is a global search hotkey — you can't type a URL containing `/` into
  a radio form field unless input mode is active (Enter first).

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
