#!/bin/sh
# faaa installer: curl -fsSL https://raw.githubusercontent.com/OkeyAmy/faaa/main/install.sh | sh
# FAAA_DIR=/somewhere picks the install dir.
# FAAA_VERSION=0.0.6 pins a release instead of "latest".
set -eu

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $os in linux|darwin) ;; *) echo "faaa: on Windows use install.ps1"; exit 1 ;; esac
case $(uname -m) in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "faaa: unsupported CPU $(uname -m)"; exit 1 ;;
esac

on_path() { case ":$PATH:" in *":$1:"*) return 0 ;; esac; return 1; }

# Pick a dir: explicit > writable dir already on PATH > ~/.local/bin
dir=${FAAA_DIR:-}
if [ -z "$dir" ]; then
  for d in "$HOME/.local/bin" "$HOME/bin" /usr/local/bin /opt/homebrew/bin; do
    if on_path "$d" && [ -w "$d" ]; then dir=$d; break; fi
  done
  dir=${dir:-$HOME/.local/bin}
fi
mkdir -p "$dir"

# Which release: "latest" unless FAAA_VERSION pins a tag. The checksums come
# from the same tag as the archive, never from a different one.
if [ -n "${FAAA_VERSION:-}" ] && [ "$FAAA_VERSION" != latest ]; then
  tag="v$(printf '%s' "$FAAA_VERSION" | sed 's/^v//')"
  base="https://github.com/OkeyAmy/faaa/releases/download/$tag"
else
  tag=latest
  base="https://github.com/OkeyAmy/faaa/releases/latest/download"
fi

asset="faaa_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
# Staged next to the real binary so the final swap is an atomic rename on the
# same filesystem. Set before the trap so the cleanup can always see it.
stage="$dir/.faaa.new.$$"
trap 'rm -rf "$tmp"; rm -f "$stage"' EXIT HUP INT TERM

curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    echo "faaa: no sha256 tool found (sha256sum, shasum or openssl)" >&2
    return 1
  fi
}

# Deliberately not `shasum -c --ignore-missing`: that flag is GNU-only and
# macOS shasum rejects it. Look up this exact asset's line and compare by hand.
want=$(grep -F -- "$asset" "$tmp/checksums.txt" | awk -v a="$asset" '$2 == a { print $1; exit }')
if [ -z "$want" ]; then
  echo "faaa: $asset is not listed in checksums.txt for $tag" >&2
  exit 1
fi
got=$(sha256_of "$tmp/$asset")
if [ "$got" != "$want" ]; then
  echo "faaa: checksum mismatch for $asset" >&2
  echo "  expected $want" >&2
  echo "  got      $got" >&2
  echo "faaa: refusing to install a corrupted or tampered download" >&2
  exit 1
fi

# Extract into the private temp dir, not over the working install: a build that
# cannot run must never be able to clobber the binary the user already has.
tar -xz -C "$tmp" -f "$tmp/$asset" faaa

# Stage inside "$dir" so the swap is a same-filesystem rename. Only a binary
# that passes the smoke test gets moved over "$dir/faaa".
cp "$tmp/faaa" "$stage"
chmod +x "$stage"

# Downloaded, verified, extracted: now prove it actually runs before claiming
# success. A wrong arch or a broken build should fail here, loudly.
if ! smoke=$("$stage" version 2>&1); then
  echo "faaa: the downloaded binary does not run" >&2
  echo "  $stage version" >&2
  if [ -n "$smoke" ]; then echo "  $smoke" >&2; fi
  echo "faaa: this release may not support $os/$arch" >&2
  echo "faaa: keeping the existing install untouched" >&2
  exit 1
fi

mv -f "$stage" "$dir/faaa"

# Put it on PATH for future shells (once, idempotent).
if ! on_path "$dir"; then
  line="export PATH=\"$dir:\$PATH\""
  case $(basename "${SHELL:-sh}") in
    zsh)  rcs="$HOME/.zshrc" ;;
    bash) if [ "$os" = darwin ]; then rcs="$HOME/.bash_profile"; else rcs="$HOME/.bashrc"; fi ;;
    fish) rcs="" ; mkdir -p "$HOME/.config/fish/conf.d"
          echo "fish_add_path $dir" > "$HOME/.config/fish/conf.d/faaa.fish" ;;
    *)    rcs="$HOME/.profile" ;;
  esac
  for rc in $rcs; do
    grep -qsF "$dir" "$rc" || printf '\n# faaa\n%s\n' "$line" >> "$rc"
  done
  echo "added $dir to your PATH (new terminals pick it up)"
fi

"$dir/faaa" install
echo "run: faaa"
on_path "$dir" || echo "      (or right now: $dir/faaa)"
