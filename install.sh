#!/bin/sh
# Install the latest nagare-go release into ~/.local/bin (or $NAGARE_INSTALL_DIR).
#
#   curl -fsSL https://raw.githubusercontent.com/nemanja-micovic/nagare-go/main/install.sh | sh
set -eu

REPO="nemanja-micovic/nagare-go"
DIR="${NAGARE_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "nagare-go: unsupported OS '$os' (Linux and macOS are supported)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "nagare-go: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
if [ -z "$tag" ]; then
  echo "nagare-go: could not find a release; build from source instead:" >&2
  echo "  git clone https://github.com/$REPO && cd nagare-go && ./compile.bash" >&2
  exit 1
fi

archive="nagare-go_${tag#v}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$tag/$archive"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading nagare-go $tag ($os/$arch)…"
curl -fsSL "$url" -o "$tmp/$archive"
tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$DIR"
install -m 0755 "$tmp/nagare-go" "$DIR/nagare-go"
echo "Installed $DIR/nagare-go"

command -v tmux >/dev/null 2>&1 || echo "Note: nagare needs tmux — install it with your package manager."
case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "Note: $DIR is not on your PATH." ;;
esac
echo
echo "Try it:  nagare-go demo"
echo "Set up:  nagare-go setup"
