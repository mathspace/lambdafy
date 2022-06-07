#!/bin/bash
set -ueo pipefail

# Download the requested lambdafy version
(
  cd /;
  echo "Setting up lambdafy version $LAMBDAFY_VERSION ..." >&2
  wget -q https://github.com/mathspace/lambdafy/releases/download/v${LAMBDAFY_VERSION}/lambdafy_${LAMBDAFY_VERSION}_linux_amd64.tar.gz
  tar -xf lambdafy_*.tar.gz
  rm -f lambdafy_*.tar.gz
)

if [ -z "${LAMBDAFY_SPEC:-}" ]; then
  unset LAMBDAFY_SPEC
fi
exec /lambdafy deploy "$1"
