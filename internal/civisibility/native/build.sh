#!/bin/bash

# Exit immediately if a command exits with a non-zero status
set -e

mkdir -p ./output

echo "Building the library for macos"
#it requires brew install sevenzip
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -ldflags '-s -w -extldflags "-static"' -o ./output/macos-arm64-libtestoptimization-static/libtestoptimization.a *.go
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-shared -ldflags '-s -w' -o ./output/macos-arm64-libtestoptimization-dynamic/libtestoptimization.dylib *.go
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -ldflags '-s -w -extldflags "-static"' -o ./output/macos-x64-libtestoptimization-static/libtestoptimization.a *.go
CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-shared -ldflags '-s -w' -o ./output/macos-x64-libtestoptimization-dynamic/libtestoptimization.dylib *.go
mkdir -p ./output/macos-libtestoptimization-static
lipo -create ./output/macos-arm64-libtestoptimization-static/libtestoptimization.a ./output/macos-x64-libtestoptimization-static/libtestoptimization.a -output ./output/macos-libtestoptimization-static/libtestoptimization.a
cp ./output/macos-arm64-libtestoptimization-static/libtestoptimization.h ./output/macos-libtestoptimization-static/libtestoptimization.h
7zz a -t7z ./output/macos-libtestoptimization-static.7z ./output/macos-libtestoptimization-static/*.*
sha256sum ./output/macos-libtestoptimization-static.7z > ./output/macos-libtestoptimization-static.7z.sha256sum
rm -r ./output/macos-libtestoptimization-static
mkdir -p ./output/macos-libtestoptimization-dynamic
lipo -create ./output/macos-arm64-libtestoptimization-dynamic/libtestoptimization.dylib ./output/macos-x64-libtestoptimization-dynamic/libtestoptimization.dylib -output ./output/macos-libtestoptimization-dynamic/libtestoptimization.dylib
cp ./output/macos-arm64-libtestoptimization-dynamic/libtestoptimization.h ./output/macos-libtestoptimization-dynamic/libtestoptimization.h
7zz a -t7z ./output/macos-libtestoptimization-dynamic.7z ./output/macos-libtestoptimization-dynamic/*.*
sha256sum ./output/macos-libtestoptimization-dynamic.7z > ./output/macos-libtestoptimization-dynamic.7z.sha256sum
rm -r ./output/macos-libtestoptimization-dynamic
rm -r ./output/macos-arm64-libtestoptimization-static ./output/macos-arm64-libtestoptimization-dynamic ./output/macos-x64-libtestoptimization-static ./output/macos-x64-libtestoptimization-dynamic

echo "Building the static library for linux-arm64"
docker build --platform linux/arm64 --build-arg GOARCH=arm64 --build-arg FILE_NAME=linux-arm64-libtestoptimization -t libtestoptimization-builder:arm64 -f ./Dockerfile ../../..
docker run --rm -v ./output:/libtestoptimization libtestoptimization-builder:arm64

echo "Building the static library for linux-x64"
docker build --platform linux/amd64 --build-arg GOARCH=amd64 --build-arg FILE_NAME=linux-x64-libtestoptimization -t libtestoptimization-builder:amd64 -f ./Dockerfile ../../..
docker run --rm -v ./output:/libtestoptimization libtestoptimization-builder:amd64

echo "Building the dynamic library for android-arm64"
docker build --platform linux/amd64 --build-arg GOARCH=arm64 --build-arg FILE_NAME=android-arm64-libtestoptimization -t libtestoptimization-builder:androidarm64 -f ./Dockerfile-android ../../..
docker run --rm -v ./output:/libtestoptimization libtestoptimization-builder:androidarm64

echo "Building the static library for ios"
CGO_LDFLAGS_ALLOW=".*" GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -ldflags '-s -w -extldflags "-static"' -o ./output/ios-libtestoptimization-static/libtestoptimization.a *.go
7zz a -t7z ./output/ios-libtestoptimization-static.7z ./output/ios-libtestoptimization-static/*.*
sha256sum ./output/ios-libtestoptimization-static.7z > ./output/ios-libtestoptimization-static.7z.sha256sum
rm -r ./output/ios-libtestoptimization-static

echo "done."