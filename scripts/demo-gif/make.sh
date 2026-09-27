#!/usr/bin/env bash
# Regenerate images/demo.gif without VHS: record nagare-go demo in an emulated
# terminal, render each frame with headless Chromium, assemble with Pillow.
#
# Needs: python3 with pyte and pillow, node with playwright, tmux, git.
set -euo pipefail
cd "$(dirname "$0")"
root=$(cd ../.. && pwd)
(cd "$root" && go build -o nagare-go .)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Overview, palette, answer a permission prompt, split twice, review a diff.
steps='[["rec",3.0],["rtype","\u000b"],["rec",0.4],["rtype","token"],["rec",0.8],["rkey","enter"],
["rec",3.0],["rkey","enter"],["rec",3.2],["rtype","\u001bv"],["rec",2.4],["rtype","\u001bv"],["rec",2.8],
["rtype","\u001b[1;3D"],["rtype","\u001b[1;3D"],["rec",0.4],["rtype","\u001bd"],["rec",2.2],
["rkey","down"],["rec",1.8],["rkey","esc"],["rec",1.0]]'

W=150 H=38 FPS=8 OUT="$work/frames.json" python3 record.py "[\"$root/nagare-go\",\"demo\",\"--speed\",\"1.5\"]" "$steps"
node render.js "$work/frames.json" "$work/png"
python3 togif.py "$work/png" "$root/images/demo.gif"
echo "wrote images/demo.gif"
