#!/usr/bin/env bash
# Builds the gdrive-sync AppImage.
#
#   ./build-appimage.sh
#
# Environment overrides:
#   GO=/path/to/go            use a specific Go toolchain (default: `go` in PATH)
#   RCLONE_BIN=/path/rclone   use an existing rclone binary instead of downloading
#   APPIMAGETOOL=/path/tool   use an existing appimagetool instead of downloading
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
BUILD="$ROOT/build"
DIST="$ROOT/dist"
GO="${GO:-go}"

case "$(uname -m)" in
  x86_64)  ARCH=x86_64 ;;
  aarch64|arm64) ARCH=aarch64 ;;
  *) echo "Nicht unterstützte Architektur: $(uname -m)" >&2; exit 1 ;;
esac

mkdir -p "$BUILD" "$DIST"

echo ">> Baue gdrive-sync (Go)…"
# cgo wird für das native Einstellungs-Fenster (WebKitGTK via dlopen) benötigt.
CGO_ENABLED=1 "$GO" build -trimpath -ldflags "-s -w" -o "$BUILD/gdrive-sync" ./cmd/gdrive-sync

# --- rclone ---
RCLONE_BIN="${RCLONE_BIN:-$BUILD/rclone}"
if [ ! -x "$RCLONE_BIN" ]; then
  echo ">> Lade rclone…"
  case "$ARCH" in
    x86_64)  RC_ARCH=amd64 ;;
    aarch64) RC_ARCH=arm64 ;;
  esac
  curl -fsSL -o "$BUILD/rclone.zip" "https://downloads.rclone.org/rclone-current-linux-${RC_ARCH}.zip"
  ( cd "$BUILD" && unzip -oq rclone.zip && cp rclone-*-linux-${RC_ARCH}/rclone rclone && rm -rf rclone-*-linux-${RC_ARCH} rclone.zip )
  chmod +x "$RCLONE_BIN"
fi

# --- appimagetool ---
APPIMAGETOOL="${APPIMAGETOOL:-$BUILD/appimagetool}"
if [ ! -x "$APPIMAGETOOL" ]; then
  echo ">> Lade appimagetool…"
  curl -fsSL -o "$APPIMAGETOOL" "https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-${ARCH}.AppImage"
  chmod +x "$APPIMAGETOOL"
fi

# --- assemble AppDir ---
echo ">> Baue AppDir…"
APPDIR="$BUILD/AppDir"
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin" \
         "$APPDIR/usr/lib/gdrive-sync" \
         "$APPDIR/usr/share/applications" \
         "$APPDIR/usr/share/icons/hicolor/scalable/apps"

cp "$BUILD/gdrive-sync"              "$APPDIR/usr/bin/gdrive-sync"
cp "$RCLONE_BIN"                     "$APPDIR/usr/lib/gdrive-sync/rclone"
cp "$ROOT/packaging/AppRun"          "$APPDIR/AppRun"
chmod +x "$APPDIR/AppRun" "$APPDIR/usr/bin/gdrive-sync" "$APPDIR/usr/lib/gdrive-sync/rclone"

cp "$ROOT/packaging/gdrive-sync.desktop" "$APPDIR/gdrive-sync.desktop"
cp "$ROOT/packaging/gdrive-sync.desktop" "$APPDIR/usr/share/applications/gdrive-sync.desktop"
cp "$ROOT/packaging/gdrive-sync.svg"     "$APPDIR/gdrive-sync.svg"
cp "$ROOT/packaging/gdrive-sync.svg"     "$APPDIR/usr/share/icons/hicolor/scalable/apps/gdrive-sync.svg"
ln -sf gdrive-sync.svg "$APPDIR/.DirIcon"

# --- build the AppImage ---
echo ">> Baue AppImage…"
OUT="$DIST/Google_Drive_Sync-${ARCH}.AppImage"
rm -f "$OUT"
ARCH="$ARCH" "$APPIMAGETOOL" --appimage-extract-and-run --no-appstream "$APPDIR" "$OUT"

echo ""
echo ">> Fertig: $OUT"
