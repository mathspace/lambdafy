#!/usr/bin/env bash
# Used to download and install lambdafy on a linux/amd64 host
set -euo pipefail
_ver="${1:-0.49}"
mkdir -p ~/bin
cd ~/bin
echo "Downloading lambdafy version $_ver ..."
wget -qO- https://github.com/mathspace/lambdafy/releases/download/v${_ver}/lambdafy_${_ver}_linux_amd64.tar.gz |
gzip -d |
tar -x lambdafy
echo "Installed lambdafy version $_ver in ~/bin/lambdafy"
