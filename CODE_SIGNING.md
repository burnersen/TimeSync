# Code signing policy

> **Status:** The Windows files of a release are not code signed yet. Free code signing
> through the [SignPath Foundation](https://signpath.org) is planned as soon as
> TimeSync is known well enough to qualify. Everything for it is prepared, and
> this policy already describes how it will work. Every release says in its
> text whether its files are signed.
>
> Once signing is active: free code signing provided by
> [SignPath.io](https://about.signpath.io), certificate by
> [SignPath Foundation](https://signpath.org).

## What is signed

Only files built from the source code in this repository, by the
[release workflow](.github/workflows/release.yml) on GitHub-hosted runners:

| File | What it is |
| ---- | ---------- |
| `TimeSync.exe` (x64, ARM64) | The window |
| `ntp-sync.exe` (x64, ARM64) | Command line tool and Windows service |
| `TimeSync-<version>-setup-x64.exe`, `-setup-arm64.exe` | The installers, built from the signed programs above |

The `.zip` files contain the same signed programs. Nothing that was built
elsewhere, and nothing from other projects, is signed with this certificate.
Every signing request is approved by hand, release by release.

The file properties of every signed file name the product `TimeSync` and the
version of the release; SignPath refuses to sign files that do not match.

## Team roles

| Role | Who |
| ---- | --- |
| Committers and reviewers | [burnersen](https://github.com/burnersen) |
| Approvers | [burnersen](https://github.com/burnersen) |

Changes from anybody else (pull requests) are reviewed by a committer before
they are merged. All team members use multi-factor authentication for GitHub
and for SignPath.

## Privacy policy

This program will not transfer any information to other networked systems
unless specifically requested by the user or the person installing or
operating it.

What TimeSync does on the network, and only for that:

- It asks the **time servers in your settings** for the time (NTP or NTS).
  The default servers are `time.cloudflare.com`, `ptbtime1.ptb.de`,
  `ptbtime2.ptb.de` and `time.windows.com`; you can change them.
- Only if NTP is blocked, it reads the time from the `Date` header of the
  **web servers in `httpsFallbackUrls`** (default: ptb.de, cloudflare.com,
  google.com, microsoft.com). This is a `HEAD` request: nothing is sent but
  the request itself.

Like any network request, these reveal your IP address to the server asked.
There is no telemetry, no update check, no account and no advertising. The
diagnostics file stays on your PC until you pass it on yourself, and masks
your user name.

## Verifying a download

- **Signature:** right-click the file > *Properties* > *Digital Signatures*.
  A signed release shows *SignPath Foundation*.
- **Checksums:** every release has a `SHA256SUMS.txt`
  (`Get-FileHash <file>` in PowerShell).

## Reporting a problem

If you think a signed file of this project does something it should not,
please open an [issue](https://github.com/burnersen/TimeSync/issues). Signed
files that break these rules can also be reported to the
[SignPath Foundation](https://signpath.org).
