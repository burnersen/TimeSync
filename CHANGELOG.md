# Changelog

## 2.0.0 (2026-10-30)

A rewrite with a window, a safer core and a proper background service.
Every command line of 1.x still works, and a 1.x service is replaced
automatically by `ntp-sync service install`.

### New

- **TimeSync.exe**, a window (German and English, light and dark): PC time
  next to the actual time, the offset in plain words, synchronize, history
  with drift statistics, settings, diagnostics file.
- **Several servers at once**: median, outliers ignored, majority required.
  Default: Cloudflare, PTB (two servers) and Microsoft, three independent
  operators. The NTP Pool is available as a preset.
- **Gentle correction**: small offsets are evened out by running the clock
  slightly faster or slower instead of jumping.
- **Synchronization after waking from sleep and after network changes**, in
  addition to start and interval.
- **NTS** (Network Time Security) with `nts://server`.
- **Web time fallback** when UDP port 123 is blocked (about 1 s accurate).
- **Explanations** for dual boot with Linux, wrong time zone, automatic
  daylight saving time switched off, weak CMOS battery, blocked NTP port.
- **Windows Time service**: taken over with a backup, restored on uninstall.
- **Company PCs** (domain members): measured only unless allowed.
- **Windows Event Viewer** entries (source "TimeSync").
- New commands: `check` (no admin needed), `status`, `history`, `config`,
  `w32time`, `diagnose`, `service start|stop|restart`; `-json` output.
- ARM64 builds, installer, winget and Scoop manifests.

### Fixed (compared with 1.0.0)

- A broken server could set the clock years into the future: a timestamp
  with zero seconds was read as February 2036 and passed the year check.
- Any single answer was trusted, however large the correction.
- Answers from servers that had never been synchronized, answers with an
  impossible round trip and stale answers were accepted.
- Kiss-of-death codes were ignored; such servers are now left alone for an
  hour (RATE) or a day (DENY, RSTR).
- The service ran as LocalSystem from wherever the file was; any user who
  could write to that folder could take over the computer. It now runs as
  Local Service from `C:\Program Files\TimeSync`.
- The log was rotated only when the service started.
- `-install` with a huge interval crashed; intervals are now limited.
- Started by double-click, the window closed before anything could be read.
- The service did not start after installation and did not restart after a
  failure.

## 1.0.0

First public release.
