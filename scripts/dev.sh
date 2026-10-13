#!/usr/bin/env sh
set -eu
cd -- "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
exec go run ./tools/dev "$@"
