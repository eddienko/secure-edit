#!/bin/sh
# Build release tarballs and checksums for every supported platform.
# usage: scripts/build-release.sh VERSION [OUTDIR]
set -eu

version=${1:?usage: build-release.sh VERSION [OUTDIR]}
out=${2:-dist}

rm -rf "$out"
mkdir -p "$out"

# Pure Go (no cgo), so every target cross-compiles from one machine.
# Windows is not supported: the lock uses flock.
for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do
	os=${target%/*}
	arch=${target#*/}
	name=sedit_${version}_${os}_${arch}
	mkdir -p "$out/$name"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch \
		go build -trimpath -ldflags "-s -w -X main.buildVersion=$version" \
		-o "$out/$name/sedit" ./cmd/sedit
	cp README.md LICENSE "$out/$name/"
	tar -C "$out" -czf "$out/$name.tar.gz" "$name"
	rm -rf "${out:?}/$name"
done

(cd "$out" && shasum -a 256 ./*.tar.gz | sed 's# \./# #' >SHA256SUMS)
ls -l "$out"
