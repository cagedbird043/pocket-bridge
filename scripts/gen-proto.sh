#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="$HOME/.local/bin:$PATH"

cd "$ROOT_DIR"
protoc \
  --proto_path=. \
  --go_out=. \
  --go_opt=module=github.com/cagedbird043/pocket-bridge \
  proto/bridge.proto
