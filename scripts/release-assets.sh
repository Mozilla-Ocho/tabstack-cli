#!/bin/sh
#
# release-assets.sh: generate the shell completions and man page that ship in
# the release archives, so Homebrew (and anyone unpacking a tarball) gets them
# without running the binary at install time. Called from .goreleaser.yaml's
# before hooks. These commands need no API key or config.
set -eu

cd "$(dirname "$0")/.."
rm -rf completions manpages
mkdir -p completions manpages

for sh in bash zsh fish; do
  go run ./cmd/tabstack completion "$sh" > "completions/tabstack.$sh"
done
go run ./cmd/tabstack man | gzip -9 -c > manpages/tabstack.1.gz
