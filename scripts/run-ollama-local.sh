#!/usr/bin/env bash
# Run Ollama locally WITHOUT sudo (no system install) — handy on Linux dev boxes
# where you can't (or don't want to) install the daemon system-wide. On macOS,
# prefer the native app/`brew install ollama` so it gets Metal GPU access.
#
# Usage:
#   scripts/run-ollama-local.sh            # download (once) + serve on :11434
#   OLLAMA_VERSION=v0.30.7 scripts/run-ollama-local.sh
#
# Then point qa-ai at it (default OLLAMA_URL=http://localhost:11434) and start
# the services — qa-ai auto-pulls qwen2.5:7b on first boot.
set -euo pipefail

DEST="${OLLAMA_DEST:-$HOME/ollama-local}"
VERSION="${OLLAMA_VERSION:-v0.30.7}"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ASSET="ollama-linux-amd64.tar.zst" ;;
  aarch64|arm64) ASSET="ollama-linux-arm64.tar.zst" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

BIN="$DEST/bin/ollama"
if [[ ! -x "$BIN" ]]; then
  echo ">> downloading ollama $VERSION ($ASSET) -> $DEST"
  mkdir -p "$DEST"
  url="https://github.com/ollama/ollama/releases/download/$VERSION/$ASSET"
  tmp="$(mktemp)"
  curl -fsSL "$url" -o "$tmp"
  tar --use-compress-program=unzstd -xf "$tmp" -C "$DEST"
  rm -f "$tmp"
fi

export PATH="$DEST/bin:$PATH"
export LD_LIBRARY_PATH="$DEST/lib/ollama:${LD_LIBRARY_PATH:-}"
export OLLAMA_HOST="${OLLAMA_HOST:-127.0.0.1:11434}"

echo ">> serving on $OLLAMA_HOST (models cached in ~/.ollama)"
exec "$BIN" serve
