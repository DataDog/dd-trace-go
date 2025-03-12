#!/bin/bash

#it requires brew install sevenzip

# Exit immediately if a command exits with a non-zero status
set -e

# Print commands and their arguments as they are executed
set -x

echo "Building the static library for macos-arm64"
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -o ./macos-arm64-libcivisibility/libcivisibility.a *.go
7zz a -t7z macos-arm64-libcivisibility.7z ./macos-arm64-libcivisibility
rm -r ./macos-arm64-libcivisibility

echo "Building the static library for macos-x64"
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -o ./macos-x64-libcivisibility/libcivisibility.a *.go
7zz a -t7z macos-x64-libcivisibility.7z ./macos-x64-libcivisibility
rm -r ./macos-x64-libcivisibility

echo "Building the static library for windows-x64"
CGO_LDFLAGS_ALLOW=".*" GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -tags civisibility_native -buildmode=c-archive -o ./windows-x64-libcivisibility/civisibility.lib *.go
7zz a -t7z windows-x64-libcivisibility.7z ./windows-x64-libcivisibility
rm -r ./windows-x64-libcivisibility

echo "Building the static library for linux-arm64"
lima CGO_LDFLAGS_ALLOW=".*" GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build -tags civisibility_native -buildmode=c-archive -o /tmp/lima/linux-arm64-libcivisibility/libcivisibility.a *.go
mv /tmp/lima/linux-arm64-libcivisibility ./linux-arm64-libcivisibility
7zz a -t7z linux-arm64-libcivisibility.7z ./linux-arm64-libcivisibility
rm -r ./linux-arm64-libcivisibility

echo "Building the static library for linux-x64"
lima CGO_LDFLAGS_ALLOW=".*" GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-linux-gnu-gcc go build -tags civisibility_native -buildmode=c-archive -o /tmp/lima/linux-x64-libcivisibility/libcivisibility.a *.go
mv /tmp/lima/linux-x64-libcivisibility ./linux-x64-libcivisibility
7zz a -t7z linux-x64-libcivisibility.7z ./linux-x64-libcivisibility
rm -r ./linux-x64-libcivisibility

echo "done."