#!/bin/sh
# faaa installer: curl -fsSL https://raw.githubusercontent.com/okeyamy/faaa/main/install.sh | sh
set -eu
REPO=okeyamy/faaa
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported arch $(uname -m)"; exit 1 ;;
esac
dir=${FAAA_DIR:-$HOME/.local/bin}
mkdir -p "$dir"
url="https://github.com/$REPO/releases/latest/download/faaa_${os}_${arch}.tar.gz"
echo "⇣ $url"
curl -fsSL "$url" | tar -xz -C "$dir" faaa
chmod +x "$dir/faaa"
"$dir/faaa" install
case ":$PATH:" in *":$dir:"*) ;; *) echo "add to PATH: export PATH=\"$dir:\$PATH\"" ;; esac
echo "run: faaa"
