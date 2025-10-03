// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

//go:build tools

package tools

import (
	_ "github.com/campoy/embedmd"
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	_ "golang.org/x/perf/cmd/benchstat"
	_ "golang.org/x/tools/cmd/goimports"
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "google.golang.org/grpc/cmd/protoc-gen-go-grpc"
	_ "google.golang.org/protobuf/cmd/protoc-gen-go"
	_ "gotest.tools/gotestsum"

	// This is a fork of the original "checklocks" analyzer that lives in the gvisor repository.
	// This is a temporary fork to allow for the development of the analyzer and testing.
	// _ "gvisor.dev/gvisor/tools/checklocks/cmd/checklocks"
	_ "github.com/kakkoyun/checklocks/cmd/checklocks"
	_ "mvdan.cc/sh/v3/cmd/shfmt"
)
