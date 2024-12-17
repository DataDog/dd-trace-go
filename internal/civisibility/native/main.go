// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

//go:build civisibility_native
// +build civisibility_native

// static libraries:
// CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -o libcivisibility.a *.go
// CGO_LDFLAGS_ALLOW=".*" GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -tags civisibility_native -buildmode=c-archive -o libcivisibility.a civisibility_exports.go
// CGO_LDFLAGS_ALLOW=".*" GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -tags civisibility_native -buildmode=c-archive -o civisibility.lib civisibility_exports.go
// CGO_LDFLAGS_ALLOW=".*" GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build -tags civisibility_native -buildmode=c-archive -o /tmp/lima/libcivisibility.a civisibility_exports.go
// CGO_LDFLAGS_ALLOW=".*" GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-linux-gnu-gcc go build -tags civisibility_native -buildmode=c-archive -o /tmp/lima/libcivisibility.a civisibility_exports.go

// dynamic libraries:
// CGO_LDFLAGS_ALLOW=".*" GOOS=android CGO_ENABLED=1 CC=$NDK_ROOT/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android21-clang go build -tags civisibility_native -buildmode=c-shared -o libcivisibility.so civisibility_exports.go
// ~/Downloads/jextract-22/bin/jextract -l ./libcivisibility.so --output classes -t com.datadog.civisibility ./libcivisibility.h

package main

func main() {}
