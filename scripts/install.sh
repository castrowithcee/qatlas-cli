#!/bin/sh
# Qatlas Connect installer for Linux, macOS and WSL.
#
#   curl -fsSL https://github.com/castrowithcee/qatlas-connect/releases/latest/download/install.sh | sh
#   curl -fsSL https://github.com/castrowithcee/qatlas-connect/releases/latest/download/install.sh | sh -s -- v1.2.3
#
# Installs the newest stable release (or the version given as argument or in QCONNECT_VERSION, including
# pre-release tags such as v1.2.3-rc.1) of qconnect into ~/.local; running it again updates the installation.
# An existing qatlas program in the prefix is replaced by a symlink to qconnect.
# The archive is installed only after the SHA-256 listed in checksums.txt matches and, when ssh-keygen is
# available, checksums.txt.qconnect.sig is a valid signature of checksums.txt by the release key embedded
# below. Any mismatch aborts before the installation is touched. The script never uses sudo and never touches
# the configuration under ~/.qatlas.
#
# Environment (all optional; the first two exist for tests and mirrors):
#   QCONNECT_VERSION                       release tag to install instead of the latest stable release
#   QCONNECT_INSTALL_BASE_URL              releases URL; only changes where files are downloaded from
#                                          (default https://github.com/castrowithcee/qatlas-connect/releases)
#   QCONNECT_INSTALL_PREFIX                installation prefix (default $HOME/.local)
#   QCONNECT_INSTALL_ALLOWED_SIGNERS_FILE  TEST ONLY: allowed-signers file used instead of the embedded key.
#                                          It replaces only the key; the signature is still required.
#   HOME, SHELL                            select the login profile that receives the PATH line
# The former QATLAS_VERSION and QATLAS_INSTALL_* variables are still read, with a warning, when the matching
# QCONNECT_ variable is unset; this fallback ends with the next major release.
set -eu

repo_url=https://github.com/castrowithcee/qatlas-connect/releases
signer_line='qatlas-release ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ6euNyMyw+0rV5TVSa24o374+wDn75ueHvdYAGZnEv5'
marker='# added by the Qatlas Connect installer'
old_marker='# added by the Qatlas installer'

fail() {
	printf 'qconnect-install: %s\n' "$*" >&2
	exit 1
}

warn() {
	printf 'qconnect-install: warning: %s\n' "$*" >&2
}

# setting <suffix> prints the value of QCONNECT_<suffix>, or of the former QATLAS_<suffix> with a warning when
# only that one is set. The new variable wins; an empty value counts as unset.
setting() {
	eval "new=\${QCONNECT_$1:-}"
	eval "old=\${QATLAS_$1:-}"
	if [ -n "$new" ]; then
		printf '%s' "$new"
	elif [ -n "$old" ]; then
		warn "QATLAS_$1 is deprecated and will be removed in the next major release; use QCONNECT_$1"
		printf '%s' "$old"
	fi
}

main() {
	[ -n "${HOME:-}" ] || fail 'HOME is not set'
	base=$(setting INSTALL_BASE_URL)
	base=${base:-$repo_url}
	base=${base%/}
	prefix=$(setting INSTALL_PREFIX)
	prefix=${prefix:-$HOME/.local}
	prefix=${prefix%/}
	signers_file=$(setting INSTALL_ALLOWED_SIGNERS_FILE)
	version=${1:-}
	[ -n "$version" ] || version=$(setting VERSION)

	command -v curl >/dev/null 2>&1 || fail 'curl is required'
	command -v tar >/dev/null 2>&1 || fail 'tar is required'

	case $(uname -s) in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "unsupported operating system $(uname -s); this installer supports Linux, macOS and WSL (use install.ps1 on Windows)" ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "unsupported architecture $(uname -m); supported: amd64, arm64" ;;
	esac

	if command -v sha256sum >/dev/null 2>&1; then
		sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
	elif command -v shasum >/dev/null 2>&1; then
		sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
	else
		fail 'sha256sum or shasum is required to verify the download'
	fi

	if [ -z "$version" ]; then
		latest=$(curl -fsSIL -o /dev/null -w '%{url_effective}' "$base/latest") || fail 'could not determine the latest release'
		version=${latest##*/}
	fi
	if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'; then
		fail "invalid version '$version'; expected a tag such as v1.2.3 or v1.2.3-rc.1"
	fi

	archive="qconnect_${version}_${os}_${arch}.tar.gz"
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/qconnect-install.XXXXXX")
	stage=
	cleanup() {
		rm -rf "$tmp"
		[ -z "$stage" ] || rm -rf "$stage"
	}
	trap cleanup EXIT
	trap 'exit 1' HUP INT TERM

	printf 'Downloading Qatlas Connect %s for %s/%s\n' "$version" "$os" "$arch"
	for name in "$archive" checksums.txt checksums.txt.qconnect.sig; do
		curl -fsSL -o "$tmp/$name" "$base/download/$version/$name" || fail "download of $name failed"
	done

	if command -v ssh-keygen >/dev/null 2>&1; then
		signers=${signers_file:-$tmp/allowed-signers}
		[ -n "$signers_file" ] || printf '%s\n' "$signer_line" >"$signers"
		ssh-keygen -Y verify -f "$signers" -I qatlas-release -n qatlas-connect-release \
			-s "$tmp/checksums.txt.qconnect.sig" <"$tmp/checksums.txt" >/dev/null 2>&1 ||
			fail 'signature of checksums.txt is invalid; nothing was installed'
	else
		warn 'ssh-keygen not found: the release signature was NOT verified, only the SHA-256 checksum'
	fi

	want=$(awk -v n="$archive" '{ f = $2; sub(/^\*/, "", f) } f == n { print tolower($1); exit }' "$tmp/checksums.txt")
	[ -n "$want" ] || fail "checksums.txt does not list $archive; nothing was installed"
	got=$(sha256 "$tmp/$archive")
	[ "$got" = "$want" ] || fail "SHA-256 mismatch for $archive; nothing was installed"

	mkdir -p "$prefix"
	stage=$(mktemp -d "$prefix/.qconnect-stage.XXXXXX")
	tar -xzf "$tmp/$archive" -C "$stage" || fail 'could not extract the archive'
	[ -f "$stage/bin/qconnect" ] && [ ! -L "$stage/bin/qconnect" ] || fail 'archive does not contain bin/qconnect'
	chmod 755 "$stage/bin/qconnect"

	# Same filesystem as the target, so every mv is an atomic rename; the program is replaced last.
	mkdir -p "$prefix/bin" "$prefix/share/man/man1" "$prefix/share/doc/qconnect"
	for page in "$stage"/share/man/man1/*.1; do
		[ -f "$page" ] || continue
		mv -f "$page" "$prefix/share/man/man1/$(basename "$page")"
	done
	for doc in "$stage"/share/doc/qconnect/*; do
		[ -f "$doc" ] || continue
		mv -f "$doc" "$prefix/share/doc/qconnect/$(basename "$doc")"
	done
	mv -f "$stage/bin/qconnect" "$prefix/bin/qconnect"

	# A previous qatlas program becomes an alias of qconnect, so existing callers keep working. The link is
	# created in the staging directory and renamed into place.
	alias_done=
	if [ -e "$prefix/bin/qatlas" ] || [ -L "$prefix/bin/qatlas" ]; then
		if [ -d "$prefix/bin/qatlas" ] && [ ! -L "$prefix/bin/qatlas" ]; then
			warn "$prefix/bin/qatlas is a directory; it was left alone"
		elif ln -s qconnect "$stage/qatlas" && mv -f "$stage/qatlas" "$prefix/bin/qatlas"; then
			alias_done=1
		else
			warn "could not replace $prefix/bin/qatlas by an alias of qconnect"
		fi
	fi

	path_changed=
	bindir=$prefix/bin
	case ":${PATH:-}:" in
	*":$bindir:"*) ;;
	*)
		if [ "$bindir" = "$HOME/.local/bin" ]; then
			path_dir='$HOME/.local/bin'
		else
			path_dir=$bindir
		fi
		shell_name=${SHELL:-}
		shell_name=${shell_name##*/}
		case $shell_name in
		zsh) profile=$HOME/.zprofile ;;
		bash)
			if [ -f "$HOME/.bash_profile" ]; then profile=$HOME/.bash_profile; else profile=$HOME/.profile; fi
			;;
		fish) profile= ;;
		*) profile=$HOME/.profile ;;
		esac
		if [ "$shell_name" = fish ]; then
			if command -v fish >/dev/null 2>&1; then
				fish -c "fish_add_path -U '$bindir'" && path_changed=1 || warn "could not update the fish PATH; add $bindir to your PATH"
			else
				warn "fish not found; add $bindir to your PATH"
			fi
		elif [ -f "$profile" ] && grep -Fq -e "$marker" -e "$old_marker" "$profile"; then
			path_changed=1
		else
			printf '\n%s\nexport PATH="%s:$PATH"\n' "$marker" "$path_dir" >>"$profile"
			path_changed=1
		fi
		;;
	esac

	printf '\nInstalled Qatlas Connect %s\n  program: %s\n' "$version" "$bindir/qconnect"
	if [ -n "$alias_done" ]; then
		printf '  alias:   %s now points to qconnect.\n' "$bindir/qatlas"
	fi
	if [ -n "$path_changed" ]; then
		printf '  PATH:    %s was added to your login profile; open a new terminal to use `qconnect`.\n' "$bindir"
	fi
	printf '\nNext: run `qconnect` (press g to connect a provider) or `qconnect web`.\n'
}

main "$@"
