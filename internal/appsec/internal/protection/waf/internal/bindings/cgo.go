// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec && cgo && !windows && amd64 && (linux || darwin)
// +build appsec
// +build cgo
// +build !windows
// +build amd64
// +build linux darwin

package bindings

// #cgo CFLAGS: -I${SRCDIR}/include
// #cgo linux,amd64 LDFLAGS: -L${SRCDIR}/lib/linux-amd64 -lddwaf -lm -ldl -Wl,-rpath=/lib64:/usr/lib64:/usr/local/lib64:/lib:/usr/lib:/usr/local/lib
// #cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/lib/darwin-amd64 -lddwaf -lstdc++
import "C"

// The following imports enforce `go mod vendor` to copy all the files we need for CGO: the WAF header file and the
// static libraries.
import (
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf/internal/bindings/include"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf/internal/bindings/lib/darwin-amd64"
	_ "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf/internal/bindings/lib/linux-amd64"
)
