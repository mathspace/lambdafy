#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

tmp=$(mktemp -d)
image="lambdafy-scratch-test-$$"
trap 'docker image rm -f "$image" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT

./build-proxy.sh
go build -o "$tmp/lambdafy" .
mkdir "$tmp/rootfs"
cat > "$tmp/app.go" <<'GO'
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() { fmt.Println(strings.Join(os.Args[1:], "|")) }
GO
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$tmp/rootfs/application" "$tmp/app.go"

for previous in absent regular symlink; do
  rm -f "$tmp/rootfs/lambdafy-proxy"
  entrypoint='ENTRYPOINT ["/application"]'
  case "$previous" in
    regular)
      printf 'stale proxy' > "$tmp/rootfs/lambdafy-proxy"
      entrypoint='ENTRYPOINT ["/lambdafy-proxy", "/application"]'
      ;;
    symlink)
      ln -s /application "$tmp/rootfs/lambdafy-proxy"
      entrypoint='ENTRYPOINT ["/lambdafy-proxy", "/application"]'
      ;;
  esac
  tar -C "$tmp/rootfs" -cf "$tmp/rootfs.tar" .
  docker image import --platform linux/amd64 \
    --change "$entrypoint" --change 'CMD ["hello", "scratch"]' \
    "$tmp/rootfs.tar" "$image" >/dev/null
  "$tmp/lambdafy" make "$image"
  test "$(docker run --rm --platform linux/amd64 "$image")" = 'hello|scratch'
  before=$(docker image inspect --format '{{.Id}}' "$image")
  "$tmp/lambdafy" make "$image"
  test "$(docker image inspect --format '{{.Id}}' "$image")" = "$before"
  echo "Scratch wrapping, argument forwarding and repeat wrapping passed ($previous proxy)."
done
