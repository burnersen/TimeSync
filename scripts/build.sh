#!/usr/bin/env bash
# Builds TimeSync for Windows from Linux, macOS or Git Bash on Windows.
#
#   scripts/build.sh 2.0.0          -> dist/TimeSync-2.0.0-windows-x64.zip and -arm64.zip
#
# Needs Go and the Wails CLI (go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0).
# The installer (Inno Setup) is built by the release workflow on Windows.
set -euo pipefail

VERSION="${1:-0.0.0-dev}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
LDFLAGS="-s -w -X github.com/burnersen/TimeSync/internal/version.Version=$VERSION"
WINVER="$(echo "$VERSION" | sed -E 's/^([0-9]+)\.([0-9]+)\.([0-9]+).*/\1.\2.\3.0/')"
case "$WINVER" in *.*.*.*) ;; *) WINVER="0.0.0.0" ;; esac

rm -rf "$DIST"
mkdir -p "$DIST"

# Icon, version and manifest for ntp-sync.exe.
(cd "$ROOT/cmd/ntp-sync" && go run github.com/tc-hib/go-winres@v0.3.3 make \
  --in winres/winres.json --out rsrc --arch amd64,arm64 \
  --product-version "${WINVER%.0}" --file-version "${WINVER%.0}")

# The window reads its version for the file properties from wails.json.
python3 - "$ROOT/gui/wails.json" "$WINVER" <<'PY' 2>/dev/null || sed -i.bak -E "s/\"productVersion\": \"[^\"]*\"/\"productVersion\": \"${WINVER%.0}\"/" "$ROOT/gui/wails.json"
import json, sys
p, v = sys.argv[1], sys.argv[2].rsplit(".", 1)[0]
d = json.load(open(p))
d["info"]["productVersion"] = v
json.dump(d, open(p, "w"), indent=2)
open(p, "a").write("\n")
PY

for arch in amd64 arm64; do
  label="x64"; [ "$arch" = arm64 ] && label="arm64"
  out="$DIST/TimeSync-$VERSION-windows-$label"
  mkdir -p "$out"
  GOOS=windows GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$out/ntp-sync.exe" "$ROOT/cmd/ntp-sync"
  (cd "$ROOT/gui" && wails build -platform "windows/$arch" -skipbindings -trimpath -ldflags "$LDFLAGS" -o "TimeSync-$arch.exe" >/dev/null)
  cp "$ROOT/gui/build/bin/TimeSync-$arch.exe" "$out/TimeSync.exe"
  cp "$ROOT/LICENSE" "$ROOT/README.md" "$ROOT/THIRD_PARTY_NOTICES.md" "$out/"
  (cd "$DIST" && rm -f "$(basename "$out").zip" && zip -qr "$(basename "$out").zip" "$(basename "$out")")
done

(cd "$DIST" && sha256sum ./*.zip > SHA256SUMS.txt)
rm -f "$ROOT"/cmd/ntp-sync/rsrc_*.syso
git -C "$ROOT" checkout -- gui/wails.json 2>/dev/null || true
echo "Done:"
ls -la "$DIST"
