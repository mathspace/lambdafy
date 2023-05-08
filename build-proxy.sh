#!/usr/bin/env bash
set -euo pipefail
cd proxy
export CGO_ENABLED=0
export GOOS=linux
export GOARCH=amd64

VER="$(
if [[ -n "$(git status --porcelain)" ]]; then
  echo "dev"
else
  git describe --tags --match "v*" --always
fi
)"
if [ -z "$VER" ]; then
  echo "Failed to generate version string by git" >&2
  exit 1
fi

go build -ldflags '-s -w' -o ../proxy-linux-amd64
