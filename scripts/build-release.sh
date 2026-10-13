#!/usr/bin/env bash
# Builds the IPMonitor release assets with the standard Go toolchain.
#
#   scripts/build-release.sh <version> [output-dir]
#
# <version> is X.Y.Z (a leading "v" is accepted and dropped). The output
# directory (default: build/release) is recreated and ends up with exactly:
#
#   ipmonitor_<version>_amd64.deb
#   ipmonitor-linux-amd64
#   ipmonitor-windows-amd64.exe
#
# Prerequisites (all present on GitHub's ubuntu-24.04 runners): bash, Go as
# required by go.mod (with network access to the module proxy on first use),
# dpkg-deb, xz, ar, file, and GNU coreutils/findutils/awk. Windows resources
# are produced by go-winres, installed at the pinned version below into a
# temporary directory; nothing is installed system-wide. No cgo, C compiler,
# Wine or MyGo CLI is involved.
set -euo pipefail
umask 022

APP_NAME=IPMonitor
APP_ID=com.f48labs.ipmonitor
PKG=ipmonitor
MAINTAINER='Factory48 Labs <325935121+Factory48@users.noreply.github.com>'
HOMEPAGE=https://github.com/Factory48/ipmonitor
GO_WINRES_VERSION=v0.3.3

die() { echo "build-release: $*" >&2; exit 1; }

[ $# -ge 1 ] && [ $# -le 2 ] || die "usage: $0 <version> [output-dir]"
VERSION=${1#v}
[[ $VERSION =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
	die "version must be X.Y.Z, got '$1'"

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUT=${2:-build/release}
case $OUT in /*) ;; *) OUT=$ROOT/$OUT ;; esac
OUT=${OUT%/}
case "$ROOT/" in "$OUT"/*) die "refusing to use '$OUT' as output directory" ;; esac

for tool in go dpkg-deb xz ar file find awk md5sum sort install od; do
	command -v "$tool" >/dev/null || die "required tool not found: $tool"
done

cd "$ROOT"
[ -f main.go ] && [ -f go.mod ] || die "not the IPMonitor repository root: $ROOT"

if [ -z "${SOURCE_DATE_EPOCH:-}" ]; then
	SOURCE_DATE_EPOCH=$(git log -1 --format=%ct HEAD 2>/dev/null || true)
	SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-1}
fi
export SOURCE_DATE_EPOCH

SYSO=$ROOT/rsrc_windows_amd64.syso
[ ! -e "$SYSO" ] || die "refusing to overwrite existing Windows resources: $SYSO"
WORK=$(mktemp -d)
cleanup() {
	rm -f "$SYSO"
	rm -rf "$WORK"
}
trap cleanup EXIT

# Identical flags for both platforms: no cgo, baseline amd64,
# trimmed paths, no build ID, no symbols/DWARF, no inlining anywhere, no
# inspector. MyGo reads the package metadata and production flag from
# these linker variables.
MYGO=github.com/egoist/mygo
LDFLAGS="-s -w -buildid="
LDFLAGS+=" -X main.appVersion=$VERSION"
LDFLAGS+=" -X $MYGO.production=1"
LDFLAGS+=" -X $MYGO.packageName=$APP_NAME"
LDFLAGS+=" -X $MYGO.packageVersion=$VERSION"
LDFLAGS+=" -X $MYGO.packageIdentifier=$APP_ID"

build() { # build <goos> <output> [extra ldflags]
	local goos=$1 out=$2 extra=${3:-}
	CGO_ENABLED=0 GOOS=$goos GOARCH=amd64 GOAMD64=v1 GOFLAGS=-mod=readonly \
		go build -trimpath -tags=mygo_noinspector -gcflags=all=-l \
		-ldflags "$LDFLAGS$extra" -o "$out" .
}

mkdir -p "$WORK/bin" "$WORK/out"
LINUX_BIN=$WORK/out/ipmonitor-linux-amd64
WINDOWS_BIN=$WORK/out/ipmonitor-windows-amd64.exe
DEB_NAME=${PKG}_${VERSION}_amd64.deb

echo ">> linux/amd64 executable"
build linux "$LINUX_BIN"

echo ">> windows/amd64 resources (go-winres $GO_WINRES_VERSION) and executable"
env -u GOOS -u GOARCH -u GOFLAGS -u GOAMD64 GOBIN="$WORK/bin" CGO_ENABLED=0 \
	go install "github.com/tc-hib/go-winres@$GO_WINRES_VERSION"
"$WORK/bin/go-winres" make \
	--in packaging/windows/winres.json \
	--out "$ROOT/rsrc" \
	--arch amd64 \
	--file-version "$VERSION" \
	--product-version "$VERSION"
[ -f "$SYSO" ] || die "go-winres did not create $SYSO"
build windows "$WINDOWS_BIN" " -H=windowsgui"
rm -f "$SYSO"

echo ">> debian package"
PKGROOT=$WORK/pkg
DOC=$PKGROOT/usr/share/doc/$PKG
ICON_DIR=$PKGROOT/usr/share/icons/hicolor/256x256/apps
install -d "$PKGROOT/DEBIAN" "$PKGROOT/usr/bin" "$PKGROOT/usr/share/applications" \
	"$ICON_DIR" "$DOC/licenses"

# The icon is installed at 256x256; fail instead of shipping a mis-sized one.
read -r -a ihdr < <(od -An -tu1 -j16 -N8 resources/icon.png)
width=$((ihdr[0] * 16777216 + ihdr[1] * 65536 + ihdr[2] * 256 + ihdr[3]))
height=$((ihdr[4] * 16777216 + ihdr[5] * 65536 + ihdr[6] * 256 + ihdr[7]))
[ "$width" = 256 ] && [ "$height" = 256 ] ||
	die "resources/icon.png must be 256x256, is ${width}x${height}"

install -m 0755 "$LINUX_BIN" "$PKGROOT/usr/bin/$PKG"
install -m 0644 packaging/linux/$APP_ID.desktop "$PKGROOT/usr/share/applications/$APP_ID.desktop"
install -m 0644 resources/icon.png "$ICON_DIR/$APP_ID.png"
install -m 0644 packaging/linux/copyright "$DOC/copyright"

# Licenses of everything linked into /usr/bin/ipmonitor: Go itself, the
# embedded fonts and flag drawings, and every third-party Go module.
LIC=$DOC/licenses
install -m 0644 "$(go env GOROOT)/LICENSE" "$LIC/go-LICENSE"
install -m 0644 resources/Go-fonts-LICENSE.txt "$LIC/go-fonts-LICENSE.txt"
install -m 0644 resources/flag-icons-LICENSE.txt "$LIC/flag-icons-LICENSE.txt"
while read -r modpath moddir; do
	found=
	for name in LICENSE LICENSE.txt LICENSE.md COPYING; do
		if [ -f "$moddir/$name" ]; then
			found=$moddir/$name
			break
		fi
	done
	[ -n "$found" ] || die "no license file in module $modpath"
	install -m 0644 "$found" "$LIC/${modpath//\//_}-LICENSE"
done < <(CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=readonly \
	go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Dir}}{{end}}{{end}}' . | sort -u)

find "$PKGROOT" -type d -exec chmod 0755 {} +
find "$PKGROOT" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +

(cd "$PKGROOT" && find usr -type f -print0 | LC_ALL=C sort -z | xargs -0 md5sum) >"$PKGROOT/DEBIAN/md5sums"
chmod 0644 "$PKGROOT/DEBIAN/md5sums"

SIZE_KB=$(find "$PKGROOT/usr" -type f -printf '%s\n' | awk '{s += $1} END {print int((s + 1023) / 1024)}')
# Runtime libraries are dlopen()ed (no cgo): GTK 3 and its GLib/Cairo/Pango/
# GdkPixbuf/ATK stack, X11 and libepoxy/GL for the GPU renderer. Names are
# listed with the pre- and post-64-bit-time_t (t64) alternatives. AppIndicator
# (tray icon) and XWayland (clipboard on Wayland sessions) are optional.
cat >"$PKGROOT/DEBIAN/control" <<EOF
Package: $PKG
Version: $VERSION
Architecture: amd64
Maintainer: $MAINTAINER
Installed-Size: $SIZE_KB
Depends: libgtk-3-0t64 | libgtk-3-0, libglib2.0-0t64 | libglib2.0-0, libcairo2, libgdk-pixbuf-2.0-0, libpango-1.0-0, libatk1.0-0t64 | libatk1.0-0, libx11-6, libepoxy0, libgl1, libegl1
Recommends: libayatana-appindicator3-1 | libappindicator3-1, libgl1-mesa-dri
Suggests: xwayland
Section: utils
Priority: optional
Homepage: $HOMEPAGE
Description: Clipboard IP address and timestamp monitor
 IPMonitor watches the clipboard and, for a public IPv4 or IPv6 address,
 shows its country or region and AS name from IPinfo Lite in a small popup.
 A 13-digit Unix millisecond timestamp is shown as local time with an
 expired / not expired colour. It lives in the tray and uses native GTK 3
 windows rendered with OpenGL; the tray icon needs AppIndicator support.
EOF
chmod 0644 "$PKGROOT/DEBIAN/control"
touch -h -d "@$SOURCE_DATE_EPOCH" "$PKGROOT/DEBIAN" "$PKGROOT/DEBIAN/control" "$PKGROOT/DEBIAN/md5sums"

dpkg-deb --root-owner-group -Zxz -z9 --build "$PKGROOT" "$WORK/out/$DEB_NAME"

echo ">> checking artifacts"
file "$LINUX_BIN" | grep -q 'ELF 64-bit LSB.*x86-64' || die "linux binary is not an amd64 ELF"
file "$WINDOWS_BIN" | grep -q 'PE32+' || die "windows binary is not a PE32+ executable"
file "$WINDOWS_BIN" | grep -q 'GUI' || die "windows binary is not a GUI subsystem executable"
file "$WINDOWS_BIN" | grep -q 'x86-64' || die "windows binary is not amd64"
grep -aq 'PerMonitorV2' "$WINDOWS_BIN" || die "windows executable lacks the DPI-aware manifest"
for bin in "$LINUX_BIN" "$WINDOWS_BIN"; do
	grep -aq "$APP_ID" "$bin" || die "$bin lacks the $APP_ID identifier"
done
ar t "$WORK/out/$DEB_NAME" | grep -qx 'control.tar.xz' || die "deb control member is not xz"
ar t "$WORK/out/$DEB_NAME" | grep -qx 'data.tar.xz' || die "deb data member is not xz"
[ "$(dpkg-deb --field "$WORK/out/$DEB_NAME" Version)" = "$VERSION" ] || die "deb version mismatch"
[ "$(dpkg-deb --field "$WORK/out/$DEB_NAME" Architecture)" = amd64 ] || die "deb architecture mismatch"

rm -rf "$OUT"
mkdir -p "$OUT"
install -m 0644 "$WORK/out/$DEB_NAME" "$OUT/$DEB_NAME"
install -m 0755 "$LINUX_BIN" "$OUT/ipmonitor-linux-amd64"
install -m 0755 "$WINDOWS_BIN" "$OUT/ipmonitor-windows-amd64.exe"

expected=$(printf '%s\n' "$DEB_NAME" ipmonitor-linux-amd64 ipmonitor-windows-amd64.exe | LC_ALL=C sort)
actual=$(find "$OUT" -mindepth 1 -printf '%P\n' | LC_ALL=C sort)
[ "$actual" = "$expected" ] || die "unexpected output contents:"$'\n'"$actual"

echo ">> $OUT"
(cd "$OUT" && ls -l)
