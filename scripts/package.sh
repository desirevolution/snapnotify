#!/bin/sh
set -eu
: "${VERSION:?VERSION is required}"
mkdir -p dist
for arch in amd64 arm64; do
  package="snapnotify_${VERSION#v}_linux_$arch"
  mkdir -p "dist/$package/sounds"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -buildvcs=false -trimpath \
    -ldflags="-s -w -X main.version=$VERSION" -o "dist/$package/snapnotify" .
  cp README.md THIRD_PARTY.md THIRD_PARTY_NOTICES.txt config.example.yaml snapserver.stream.conf "dist/$package/"
  tar -C dist -czf "dist/$package.tar.gz" "$package"
done
(cd dist && sha256sum ./*.tar.gz > checksums.txt)
