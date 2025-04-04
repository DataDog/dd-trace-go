// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build tools

package kubernetes // import "github.com/DataDog/dd-trace-go/contrib/k8s.io/client-go/v2/kubernetes"

// This package is imported by code injected by `orchestrion.tool.go` but is otherwise not part of
// the dependency closure of the package. This ensures the `go mod tidy` closure contains everything
// that is necessary.
import _ "k8s.io/client-go/transport"
