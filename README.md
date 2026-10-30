# TimeSync

Keeps your Windows clock accurate, and tells you in plain words what is wrong
when it isn't.

TimeSync asks several time servers at once, takes the median, ignores
outliers and only sets the clock when the servers agree. A small background
service keeps it that way: after Windows starts, every hour, after waking
from sleep and after a network change. The window shows your PC's time next
to the actual time and explains the usual suspects: dual boot with Linux, a
wrong time zone, automatic daylight saving time switched off, a weak CMOS
battery, a company network that blocks the time port.

🌐 Website: [tech.burnersen.de/en/tools/timesync-en](https://tech.burnersen.de/en/tools/timesync-en/) · 🇩🇪 Auf Deutsch: [tech.burnersen.de/tools/timesync](https://tech.burnersen.de/tools/timesync/)

## What's in the box

| File | What it is |
| ---- | ---------- |
| `TimeSync.exe` | The window. Runs as a normal user; asks for administrator rights (UAC) only when needed. |
| `ntp-sync.exe` | Command line tool and Windows service. Every command line of version 1.x still works. |

Requirements: Windows 10 or 11, x64 or ARM64. The window uses the Microsoft
Edge WebView2 runtime, which is part of current Windows.

## Install

* **Installer (recommended):** download `TimeSync-<version>-setup-x64.exe`
  (or `-arm64`) from the [latest release](https://github.com/burnersen/TimeSync/releases/latest)
  and run it. Leave "keep the clock in sync in the background" ticked.
* **Portable:** download the `.zip`, unpack it anywhere and start
  `TimeSync.exe`. Setting up the background service copies `ntp-sync.exe`
  to `C:\Program Files\TimeSync`, where only administrators can change it.
  To keep the window's preferences next to the program, create an empty
  file `portable.txt` beside `TimeSync.exe`.
* **winget / Scoop:** coming soon (manifests are in [`packaging/`](packaging/)).

> **Windows SmartScreen** may warn because the files are not yet digitally
> signed. Choose "More info" → "Run anyway". The source code is all here,
> and every release is built by GitHub Actions from this repository.

## How it decides

Every synchronization round:

1. All configured servers are asked at the same time. The default comes
   from three operators that offer public time service: Cloudflare
   (`time.cloudflare.com`), the German national metrology institute PTB
   (`ptbtime1.ptb.de`, `ptbtime2.ptb.de`) and Microsoft (`time.windows.com`).
   The [NTP Pool](https://www.ntppool.org) is one click away in the
   settings (`0.pool.ntp.org` to `3.pool.ntp.org`).
2. Each answer is checked: protocol fields, kiss-of-death codes, leap
   indicator, stratum, root distance, freshness, a plausible date, an
   answer that cannot have been sent before it was received, and a random
   request token that a forged answer cannot know.
3. The median of the valid answers is the reference; answers too far from
   it are ignored as outliers. More than half of the answers must agree,
   otherwise nothing is changed.
4. Corrections larger than 15 minutes need at least three agreeing servers
   (a dead CMOS battery that reset the clock to 2001 is fixed, a single
   broken server claiming it is 2036 is not).
5. Differences under 2 ms are left alone. Up to 500 ms the service runs the
   clock 0.05 % faster or slower until the difference is gone; nobody
   notices. Larger differences are corrected directly.
6. If no NTP server answers at all (UDP port 123 blocked), the time is taken
   from the `Date` header of a few well-known web servers instead, accurate
   to about one second. This can be switched off.

Servers can be prefixed with `nts://` to use [Network Time
Security](https://www.rfc-editor.org/rfc/rfc8915) (encrypted, authenticated
NTP), for example `nts://ptbtime1.ptb.de` or `nts://time.cloudflare.com`.

## The background service

`ntp-sync service install` (or the installer, or the button in the window):

* copies `ntp-sync.exe` to `C:\Program Files\TimeSync`,
* creates the service **TimeSync**: account *Local Service* with its own
  service SID, delayed automatic start, restart after a failure,
* stores settings in `C:\ProgramData\TimeSync\config.json` (only
  administrators may change them, everyone may read them) and its log and
  history in `C:\ProgramData\TimeSync\state\`,
* replaces a version 1.x service named `ntp-sync`, if present.

### Windows Time service (w32time)

By default TimeSync takes over: the Windows Time service keeps running
(other parts of Windows use it), but its time source is set to `NoSync`, so
only one program sets the clock. The original setting is saved to
`C:\ProgramData\TimeSync\w32time-backup.json` and written back when the
service is removed. Alternatively, set `w32timeMode` to `leave`.

### Company computers

On a computer that belongs to an Active Directory domain, the domain
controller is the time authority and signing in depends on it. TimeSync
therefore only measures and reports there, and leaves Windows Time alone.
An administrator can allow it anyway (`allowOnDomain`, or the checkbox in
the settings).

## Dual boot with Linux

If the clock is off by exactly your time zone offset (one or two hours in
Germany) after starting Windows, Linux and Windows disagree about the
hardware clock: Linux keeps it in UTC, Windows in local time. TimeSync fixes
the clock after every start, but the clean solution is to make both agree.
Either tell Linux to use local time:

```
timedatectl set-local-rtc 1
```

or tell Windows to use UTC (as administrator, then restart):

```
reg add "HKLM\SYSTEM\CurrentControlSet\Control\TimeZoneInformation" /v RealTimeIsUniversal /t REG_DWORD /d 1 /f
```

To undo the Windows change:

```
reg delete "HKLM\SYSTEM\CurrentControlSet\Control\TimeZoneInformation" /v RealTimeIsUniversal /f
```

## Command line

```
ntp-sync                      synchronize once (like "sync", with an overview)
ntp-sync check                only measure how far off the clock is (no admin needed)
ntp-sync sync                 synchronize now (via the service if it runs, else admin needed)
ntp-sync status               show the service status
ntp-sync history [-n 20]      last synchronizations and the clock's drift
ntp-sync run [-interval 60]   keep synchronizing in this window (admin)
ntp-sync diagnose [-out FILE] diagnostics report for support requests

ntp-sync service install [-interval 60]   install the background service (admin)
ntp-sync service uninstall [-purge]       remove it and restore Windows Time (admin)
ntp-sync service start|stop|restart

ntp-sync config show [-json]
ntp-sync config set servers=nts://ptbtime1.ptb.de,nts://ptbtime2.ptb.de,0.pool.ntp.org interval=30
ntp-sync config reset
ntp-sync config import FILE
ntp-sync w32time status|restore
```

Options: `-server a,b,c` (use other servers once), `-json` (machine readable),
`-force` (set the clock on a domain computer), `-local` (don't go through the
service).

Exit codes: `0` ok, `1` error, `2` wrong usage, `3` no trustworthy time,
`4` administrator rights needed.

Version 1.x command lines (`-install 60`, `-uninstall`, `-interval 30`,
`-version`) keep working, and a service installed by 1.x keeps running after
the program file is replaced.

### Settings

`C:\ProgramData\TimeSync\config.json`:

| Key | Default | Meaning |
| --- | --- | --- |
| `servers` | Cloudflare, PTB (2), Microsoft | NTP servers; `host`, `host:port`, `nts://host` |
| `intervalMinutes` | 60 | regular interval (15 to 1440) |
| `syncOnStart`, `syncOnResume`, `syncOnNetworkChange` | true | extra synchronizations |
| `minCorrectionMs` | 2 | smaller differences are left alone |
| `slewMaxMs` | 500 | gentle correction up to this size; 0 always steps |
| `bigJumpMinutes` | 15 | larger corrections need three agreeing servers |
| `httpsFallback`, `httpsFallbackUrls` | true, four sites | web time when NTP is blocked |
| `w32timeMode` | `takeover` | or `leave` |
| `allowOnDomain` | false | set the clock on domain computers too |
| `eventLog` | true | write to the Windows Event Viewer (source "TimeSync") |

## Privacy

TimeSync contacts only the time servers in your settings and, if NTP is
blocked, the web servers in `httpsFallbackUrls` (a `HEAD` request, nothing
is sent but the request itself). There is no telemetry, no update check and
no account. The diagnostics file masks your user name.

## Uninstall

Use "Apps" in the Windows settings (installer), or run
`ntp-sync service uninstall` as administrator (portable) and delete the
folder. Either way the Windows Time service is set back to how it was.
`-purge` also deletes settings, log and history.

## Building

Needs [Go](https://go.dev) and, for the window, the
[Wails](https://wails.io) CLI v2.

```
git clone https://github.com/burnersen/TimeSync.git
cd TimeSync
go test ./...
for c in gui/frontend/*_check.js; do node "$c"; done
scripts/build.sh 2.0.0
```

Releases are built by [`.github/workflows/release.yml`](.github/workflows/release.yml)
when a `v*` tag is pushed; the result is a draft release with installers,
zip files and `SHA256SUMS.txt`.

Layout: `cmd/ntp-sync` (command line and service), `gui` (the window),
`internal/source` (asking servers), `internal/engine` (deciding),
`internal/syncer` (one round), `internal/service`, `internal/winsys`
(Windows calls), `internal/ipc` (named pipe between window and service).

## License

[MIT](LICENSE). Third-party components: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
