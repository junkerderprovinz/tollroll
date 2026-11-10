#!/bin/bash
# Builds the TollRoll plugin package (.txz) from the service binary and the
# webgui files. It uses tar instead of Slackware's makepkg, so it runs the same
# in CI and locally.
#
#   plugin/pkg_build.sh [VERSION]      # VERSION defaults to today (YYYY.MM.DD)
#
# Output: plugin/out/tollroll-<version>-x86_64-1.txz and its .sha256.
set -euo pipefail

VERSION="${1:-$(date +%Y.%m.%d)}"
ARCH="x86_64"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PLUGIN_SRC="$ROOT/plugin/src/tollroll"
BIN_REL="usr/local/emhttp/plugins/tollroll/bin/tollroll"
OUT="$ROOT/plugin/out"
PKGROOT="$(mktemp -d)"
trap 'rm -rf "$PKGROOT"' EXIT

echo "==> assembling package tree"
cp -a "$PLUGIN_SRC/." "$PKGROOT/"

echo "==> building the service (linux/amd64)"
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$PKGROOT/$BIN_REL" ./cmd/tollroll )
chmod +x "$PKGROOT/$BIN_REL" "$PKGROOT/usr/local/emhttp/plugins/tollroll/scripts/rc.tollroll"

# Unraid's PageBuilder drops a CRLF .page, and a trailing CR breaks shebangs.
# .gitattributes covers checkouts; this covers a Windows autocrlf tree.
echo "==> normalising text files to LF"
find "$PKGROOT" -type f ! -path "*/bin/*" ! -name '*.png' -print0 \
  | while IFS= read -r -d '' f; do perl -i -pe 's/\r\n/\n/g; s/\r$//' "$f"; done

mkdir -p "$OUT"
TXZ="$OUT/tollroll-$VERSION-$ARCH-1.txz"
echo "==> packaging $TXZ"
# --force-local keeps GNU tar from reading the colon in "D:/..." as a remote
# host. Every entry, "./" included, has to be root:root, because upgradepkg
# applies the owner of "./" to / and a wrong owner there breaks sshd key auth.
tar --force-local --owner=0 --group=0 --numeric-owner -C "$PKGROOT" -caf "$TXZ" .

echo "==> sha256"
# A bare filename in the .sha256 lets `sha256sum -c` work after a download.
( cd "$OUT" && b="$(basename "$TXZ")" && sha256sum "$b" | tee "$b.sha256" )
echo "done: $TXZ"
