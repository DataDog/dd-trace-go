#!/bin/bash

#it requires brew install sevenzip

# Exit immediately if a command exits with a non-zero status
set -e

# Print commands and their arguments as they are executed
set -x

mkdir -p ./output

echo "Building the static library for macos-arm64"
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -o ./output/macos-arm64-libcivisibility/libcivisibility.a *.go
7zz a -t7z ./output/macos-arm64-libcivisibility.7z ./output/macos-arm64-libcivisibility
sha256sum ./output/macos-arm64-libcivisibility.7z > ./output/macos-arm64-libcivisibility.7z.sha256
rm -r ./output/macos-arm64-libcivisibility

echo "Building the static library for macos-x64"
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -o ./output/macos-x64-libcivisibility/libcivisibility.a *.go
7zz a -t7z ./output/macos-x64-libcivisibility.7z ./output/macos-x64-libcivisibility
sha256sum ./output/macos-x64-libcivisibility.7z > ./output/macos-x64-libcivisibility.7z.sha256
rm -r ./output/macos-x64-libcivisibility

echo "Building the static library for windows-x64"
CGO_LDFLAGS_ALLOW=".*" GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -tags civisibility_native -buildmode=c-archive -o ./output/windows-x64-libcivisibility/civisibility.lib *.go
7zz a -t7z ./output/windows-x64-libcivisibility.7z ./output/windows-x64-libcivisibility
sha256sum ./output/windows-x64-libcivisibility.7z > ./output/windows-x64-libcivisibility.7z.sha256
rm -r ./output/windows-x64-libcivisibility

echo "Building the static library for linux-arm64"
docker build --platform linux/arm64 --build-arg GOARCH=arm64 --build-arg FILE_NAME=linux-arm64-libcivisibility -t libcivisibility-builder:arm64 -f ./Dockerfile ../../..
docker run --rm -v ./output:/libcivisibility libcivisibility-builder:arm64
#lima CGO_LDFLAGS_ALLOW=".*" GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build -tags civisibility_native -buildmode=c-archive -o /tmp/lima/linux-arm64-libcivisibility/libcivisibility.a *.go
#mv /tmp/lima/linux-arm64-libcivisibility ./linux-arm64-libcivisibility
#7zz a -t7z linux-arm64-libcivisibility.7z ./linux-arm64-libcivisibility
#rm -r ./linux-arm64-libcivisibility

echo "Building the static library for linux-x64"
docker build --platform linux/amd64 --build-arg GOARCH=amd64 --build-arg FILE_NAME=linux-x64-libcivisibility -t libcivisibility-builder:amd64 -f ./Dockerfile ../../..
docker run --rm -v ./output:/libcivisibility libcivisibility-builder:amd64
#lima CGO_LDFLAGS_ALLOW=".*" GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-linux-gnu-gcc go build -tags civisibility_native -buildmode=c-archive -o /tmp/lima/linux-x64-libcivisibility/libcivisibility.a *.go
#mv /tmp/lima/linux-x64-libcivisibility ./linux-x64-libcivisibility
#7zz a -t7z linux-x64-libcivisibility.7z ./linux-x64-libcivisibility
#rm -r ./linux-x64-libcivisibility

echo "done."