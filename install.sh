#!/bin/sh
# faaa installer: curl -fsSL https://raw.githubusercontent.com/OkeyAmy/faaa/main/install.sh | sh
# FAAA_DIR=/somewhere picks the install dir.
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

url="https://github.com/OkeyAmy/faaa/releases/latest/download/faaa_${os}_${arch}.tar.gz"
curl -fsSL "$url" | tar -xz -C "$dir" faaa
chmod +x "$dir/faaa"

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
