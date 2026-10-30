# winget manifests

Templates for the [winget-pkgs](https://github.com/microsoft/winget-pkgs) repository.

For each release:

1. Replace `{{VERSION}}` with the version (for example `2.0.0`).
2. Replace `{{DATE}}` with the release date (YYYY-MM-DD).
3. Replace `{{SHA256_X64}}` and `{{SHA256_ARM64}}` with the hashes of the two
   setup files from `SHA256SUMS.txt` of the release (in capitals).
4. Check them: `winget validate --manifest packaging/winget/`
5. Test locally: `winget install --manifest packaging/winget/`
6. Submit them to winget-pkgs under `manifests/b/burnersen/TimeSync/<version>/`,
   by hand or with `wingetcreate update burnersen.TimeSync --version <v> --urls <x64-url> <arm64-url> --submit`.

The installer type is Inno Setup, so winget installs silently, and the
installer's uninstaller removes the background service and restores the
Windows Time service. With a portable package winget could not do that.
