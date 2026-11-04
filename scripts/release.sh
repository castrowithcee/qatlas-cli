#!/bin/sh
set -eu

version=${1:-}
output=${2:-dist}

if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'; then
	printf 'release: version must be a SemVer tag such as v0.1.0 or v0.1.0-rc.1\n' >&2
	exit 2
fi

if [ -d "$output" ] && [ -n "$(find "$output" -mindepth 1 -maxdepth 1 -print -quit)" ]; then
	printf 'release: output directory is not empty: %s\n' "$output" >&2
	exit 2
fi

mkdir -p "$output"
output=$(cd "$output" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM

if [ -z "${SOURCE_DATE_EPOCH:-}" ]; then
	SOURCE_DATE_EPOCH=$(git show -s --format=%ct "${version}^{commit}")
	export SOURCE_DATE_EPOCH
fi

man_dir="$work/man"
mkdir -p "$man_dir"
go run ./cmd/gen-man -version "$version" -output "$man_dir"

targets='linux amd64
linux arm64
darwin amd64
darwin arm64
windows amd64'

# Each target is built once. The same binary goes into the qconnect archive and, until the next major release,
# into an archive under the old name qatlas in the layout the previous updater expects (bin/qatlas,
# share/man/man1/qatlas.1, share/doc/qatlas/).
pack() {
	# pack <stage> <archive> <windows|unix>
	if [ "$3" = windows ]; then
		(cd "$1" && zip -q -r "$output/$2" .)
	else
		tar -C "$1" -czf "$output/$2" .
	fi
}

printf '%s\n' "$targets" | while read -r goos goarch; do
	stage="$work/stage-$goos-$goarch"
	legacy="$work/legacy-$goos-$goarch"
	mkdir -p "$stage/bin" "$legacy/bin"
	binary=qconnect
	legacy_binary=qatlas
	ext=tar.gz
	if [ "$goos" = windows ]; then
		binary=qconnect.exe
		legacy_binary=qatlas.exe
		ext=zip
	fi

	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
		-ldflags "-s -w -X github.com/castrowithcee/qatlas-connect/internal/cli.version=$version" \
		-o "$stage/bin/$binary" .
	cp "$stage/bin/$binary" "$legacy/bin/$legacy_binary"
	mkdir -p "$stage/share/doc/qconnect" "$legacy/share/doc/qatlas"
	cp LICENSE "$stage/share/doc/qconnect/LICENSE"
	cp LICENSE "$legacy/share/doc/qatlas/LICENSE"
	if [ "$goos" != windows ]; then
		mkdir -p "$stage/share/man/man1" "$legacy/share/man/man1"
		cp "$man_dir"/*.1 "$stage/share/man/man1/"
		cp "$man_dir/qconnect.1" "$legacy/share/man/man1/qatlas.1"
	fi
	pack "$stage" "qconnect_${version}_${goos}_${goarch}.$ext" "$goos"
	pack "$legacy" "qatlas_${version}_${goos}_${goarch}.$ext" "$goos"
done

# The installers are release assets and are listed in checksums.txt, so the signature covers them. install.sh
# is required; install.ps1 is copied as soon as it exists.
scripts_dir=$(dirname "$0")
cp "$scripts_dir/install.sh" "$output/install.sh"
if [ -f "$scripts_dir/install.ps1" ]; then
	cp "$scripts_dir/install.ps1" "$output/install.ps1"
fi

if command -v sha256sum >/dev/null 2>&1; then
	(cd "$output" && sha256sum qconnect_* qatlas_* install.*) > "$output/checksums.txt"
else
	(cd "$output" && shasum -a 256 qconnect_* qatlas_* install.*) > "$output/checksums.txt"
fi
