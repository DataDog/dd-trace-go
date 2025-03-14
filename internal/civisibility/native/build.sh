#!/bin/bash

#it requires brew install sevenzip

# Exit immediately if a command exits with a non-zero status
set -e

# Print commands and their arguments as they are executed
set -x

mkdir -p ./output

echo "Building the static library for macos-arm64"
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -ldflags '-s -w -extldflags "-static"' -o ./output/macos-arm64-libcivisibility-static/libcivisibility.a *.go
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-shared -ldflags '-s -w' -o ./output/macos-arm64-libcivisibility-dynamic/libcivisibility.dylib *.go
7zz a -t7z ./output/macos-arm64-libcivisibility-static.7z ./output/macos-arm64-libcivisibility-static
7zz a -t7z ./output/macos-arm64-libcivisibility-dynamic.7z ./output/macos-arm64-libcivisibility-dynamic
sha256sum ./output/macos-arm64-libcivisibility-static.7z > ./output/macos-arm64-libcivisibility-static.7z.sha256
sha256sum ./output/macos-arm64-libcivisibility-dynamic.7z > ./output/macos-arm64-libcivisibility-dynamic.7z.sha256
rm -r ./output/macos-arm64-libcivisibility-static ./output/macos-arm64-libcivisibility-dynamic

echo "Building the static library for macos-x64"
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -ldflags '-s -w -extldflags "-static"' -o ./output/macos-x64-libcivisibility-static/libcivisibility.a *.go
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-shared -ldflags '-s -w' -o ./output/macos-x64-libcivisibility-dynamic/libcivisibility.a *.go
7zz a -t7z ./output/macos-x64-libcivisibility-static.7z ./output/macos-x64-libcivisibility-static
7zz a -t7z ./output/macos-x64-libcivisibility-dynamic.7z ./output/macos-x64-libcivisibility-dynamic
sha256sum ./output/macos-x64-libcivisibility-static.7z > ./output/macos-x64-libcivisibility-static.7z.sha256
sha256sum ./output/macos-x64-libcivisibility-dynamic.7z > ./output/macos-x64-libcivisibility-dynamic.7z.sha256
rm -r ./output/macos-x64-libcivisibility-static ./output/macos-x64-libcivisibility-dynamic

echo "Building the static library for windows-x64"
CGO_LDFLAGS_ALLOW=".*" GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -tags civisibility_native -buildmode=c-archive -ldflags '-s -w -extldflags "-static"' -o ./output/windows-x64-libcivisibility-static/civisibility.lib *.go
CGO_LDFLAGS_ALLOW=".*" GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -tags civisibility_native -buildmode=c-shared -ldflags '-s -w' -o ./output/windows-x64-libcivisibility-dynamic/civisibility.dll *.go
7zz a -t7z ./output/windows-x64-libcivisibility-static.7z ./output/windows-x64-libcivisibility-static
7zz a -t7z ./output/windows-x64-libcivisibility-dynamic.7z ./output/windows-x64-libcivisibility-dynamic
sha256sum ./output/windows-x64-libcivisibility-static.7z > ./output/windows-x64-libcivisibility-static.7z.sha256
sha256sum ./output/windows-x64-libcivisibility-dynamic.7z > ./output/windows-x64-libcivisibility-dynamic.7z.sha256
rm -r ./output/windows-x64-libcivisibility-static ./output/windows-x64-libcivisibility-dynamic

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