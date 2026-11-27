#!/usr/bin/env bash
# Builds a release bundle for the current OS/arch into dist/:
#   macOS:   acat.app (helper embedded in Contents/Frameworks) → .dmg (+ .zip)
#   Windows: acat.exe + acat-video\ → .zip
#   Linux:   acat + acat-video/ → .tar.gz
# Usage: scripts/package.sh <version>   (run from the repo root)
# Optional signing (macOS): MACOS_SIGN_IDENTITY, and for notarization
# APPLE_ID, APPLE_TEAM_ID, APPLE_APP_PASSWORD.
# APP_ONLY=1 (macOS): stop after dist/acat.app, no .dmg (used by make install).
set -euo pipefail

VERSION="${1:?version}"
VERSION="${VERSION#v}"
ROOT="$(pwd)"
OUT="$ROOT/dist"
WORK="$ROOT/dist/work"
PKG=github.com/ape-dev-de/acat/internal/version
LDFLAGS="-s -w -X $PKG.Version=$VERSION -X $PKG.Commit=$(git rev-parse --short HEAD) -X $PKG.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
[ -n "${ACAT_DEFAULT_CLIENT_ID:-}" ] && LDFLAGS="$LDFLAGS -X github.com/ape-dev-de/acat/internal/config.DefaultClientID=$ACAT_DEFAULT_CLIENT_ID"
[ -n "${ACAT_DEFAULT_RELAY_URL:-}" ] && LDFLAGS="$LDFLAGS -X github.com/ape-dev-de/acat/internal/config.DefaultRelayURL=$ACAT_DEFAULT_RELAY_URL"

GOOS="$(go env GOOS)"
GOARCH="$(go env GOARCH)"
NAME="acat_${VERSION}_${GOOS}_${GOARCH}"
rm -rf "$WORK" && mkdir -p "$WORK" "$OUT"

echo "==> video helper"
# Reinstall only when the lockfile is newer than the installed tree.
(cd video && { [ node_modules/.package-lock.json -nt package-lock.json ] || npm ci --no-audit --no-fund; } && CSC_IDENTITY_AUTO_DISCOVERY=false npm run dist -- "-c.extraMetadata.version=$VERSION")
HELPER_DIR="$(ls -d video/release/*/ | grep -v builder | head -1)"

echo "==> client"
EXE=acat
[ "$GOOS" = windows ] && EXE=acat.exe
go build -trimpath -ldflags "$LDFLAGS" -o "$WORK/bin/$EXE" ./cmd/acat

notices() {
  cp LICENSE NOTICE "$1/"
  cp docs/THIRD_PARTY.md "$1/THIRD_PARTY.md"
}

case "$GOOS" in
darwin)
  APP="$WORK/acat.app"
  mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Frameworks" "$APP/Contents/Resources"
  mv "$WORK/bin/acat" "$APP/Contents/MacOS/acat"
  ditto "$HELPER_DIR/acat-video.app" "$APP/Contents/Frameworks/acat-video.app"
  sed "s/@VERSION@/$VERSION/g" packaging/macos/Info.plist > "$APP/Contents/Info.plist"
  notices "$APP/Contents/Resources"
  if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
    ENT=video/build/entitlements.mac.plist
    # inside-out: helper frameworks/apps first, then the helper, then acat
    find "$APP/Contents/Frameworks/acat-video.app/Contents/Frameworks" -maxdepth 1 \( -name "*.app" -o -name "*.framework" \) -print0 |
      xargs -0 -I{} codesign --force --timestamp --options runtime --entitlements "$ENT" --sign "$MACOS_SIGN_IDENTITY" {}
    codesign --force --timestamp --options runtime --entitlements "$ENT" --sign "$MACOS_SIGN_IDENTITY" "$APP/Contents/Frameworks/acat-video.app"
    codesign --force --timestamp --options runtime --sign "$MACOS_SIGN_IDENTITY" "$APP"
  else
    echo "!! MACOS_SIGN_IDENTITY not set: bundle is unsigned (ad-hoc)"
    codesign --force --deep --sign - "$APP"
  fi
  if [ "${APP_ONLY:-}" = 1 ]; then
    rm -rf "$OUT/acat.app"
    mv "$APP" "$OUT/acat.app"
    rm -rf "$WORK"
    echo "==> $OUT/acat.app"
    exit 0
  fi
  hdiutil create -quiet -volname acat -srcfolder "$APP" -fs HFS+ -format ULMO -ov "$OUT/$NAME.dmg"
  if [ -n "${APPLE_ID:-}" ] && [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
    xcrun notarytool submit "$OUT/$NAME.dmg" --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_PASSWORD" --wait
    xcrun stapler staple "$OUT/$NAME.dmg"
  fi
  ;;
windows)
  D="$WORK/acat"
  mkdir -p "$D"
  mv "$WORK/bin/acat.exe" "$D/"
  cp -R "$HELPER_DIR" "$D/acat-video"
  notices "$D"
  (cd "$WORK" && 7z a -tzip -mx=9 "$OUT/$NAME.zip" acat >/dev/null)
  ;;
linux)
  D="$WORK/acat"
  mkdir -p "$D"
  mv "$WORK/bin/acat" "$D/"
  cp -R "$HELPER_DIR" "$D/acat-video"
  cp packaging/linux/acat.desktop "$D/"
  notices "$D"
  tar -C "$WORK" -czf "$OUT/$NAME.tar.gz" acat
  ;;
esac

rm -rf "$WORK"
ls -la "$OUT"
