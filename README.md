# ntp-sync

A tiny NTP time synchronizer for Windows. One small executable, no
installer, no dependencies — run it once, keep it running in a console
window, or install it as a native Windows service with a single command.

🌐 Website: [tech.burnersen.de/en/tools/timesync-en](https://tech.burnersen.de/en/tools/timesync-en/) · 🇩🇪 Auf Deutsch: [tech.burnersen.de/tools/timesync](https://tech.burnersen.de/tools/timesync/)

## Features

- **Three modes:** one-shot sync, foreground loop, or Windows service
- **Accurate:** implements the standard NTP four-timestamp exchange
  (RFC 5905), so network latency is measured and compensated
- **Safe:** every server response is validated (packet size, mode,
  stratum, leap indicator, echoed originate timestamp, plausible date
  range) before the clock is touched — a broken or spoofed reply can
  never set your clock to a nonsense time
- **Boot-friendly:** right after Windows starts, DNS is often not ready
  yet; the first synchronization is retried automatically instead of
  waiting a full interval
- **Transparent:** the service logs every sync with the measured clock
  offset to `ntp_sync.log` next to the executable (rotated at 1 MB)

## Requirements

- Windows 10/11 (64-bit)
- Administrator privileges (changing the system time requires them)

## Installation

**Option 1 — Download:** get `ntp-sync.exe` from the
[Releases](../../releases) page and put it in a permanent folder of your
choice, e.g. `C:\Tools\ntp-sync\`.

**Option 2 — Build from source** (requires [Go](https://go.dev)):

```
git clone https://github.com/burnersen/ntp-sync.git
cd ntp-sync
go build -ldflags "-s -w" -o ntp-sync.exe .
```

## Usage

Open a console **as administrator**, then:

| Command | Effect |
| ------- | ------ |
| `ntp-sync.exe` | Synchronize the clock once, right now |
| `ntp-sync.exe -interval 30` | Keep syncing every 30 minutes in this console window |
| `ntp-sync.exe -install 60` | Install as a Windows service that syncs every 60 minutes |
| `ntp-sync.exe -uninstall` | Remove the Windows service |
| `ntp-sync.exe -version` | Print the version |

Running `ntp-sync.exe` without arguments also prints this overview before
synchronizing.

### Running as a Windows service

`ntp-sync.exe -install 30` registers a service named **ntp-sync** with
automatic start, so it synchronizes the clock in the background from the
next boot on. To start it immediately without rebooting:

```
sc start ntp-sync
```

The service writes its log to `ntp_sync.log` in the same folder as the
executable. Keep the executable in a permanent location — the service
points to wherever it was installed from.

A typical log looks like this:

```
2026/07/06 08:27:30 [INFO] Service started. Sync interval: 30m0s
2026/07/06 08:27:31 [NTP] Initial synchronization successful, clock offset was 19ms
2026/07/06 08:57:31 [NTP] Synchronization successful, clock offset was -4ms
```

### Note on the built-in Windows Time service

Windows ships its own time service (W32Time). Both can coexist, but if
you install ntp-sync you may want to disable W32Time so that only one
component adjusts the clock: open `services.msc`, set **Windows Time** to
*Disabled* (or run `sc config w32time start= disabled` as administrator).

## How it works

ntp-sync sends a standard NTP client request to `pool.ntp.org` (UDP port
123), validates the reply, and computes the clock offset from the four
NTP timestamps. It then adjusts the Windows system clock through the
native `SetSystemTime` API. That's all — no background magic, a single
source file you can read in a few minutes.

## License

[MIT](LICENSE)
