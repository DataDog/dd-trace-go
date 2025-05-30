// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// Package os provides integrations into the standard library's `os` package,
// allowing protection against Local File Inclusion (LFI) attacks.
package os

// These imports satisfy injected dependencies for Orchestrion auto instrumentation.
import (
	"context"
	"os"

	v2 "github.com/DataDog/dd-trace-go/v2/contrib/os"
)

// OpenFile is a [context.Context]-aware version of [os.OpenFile], that allows
// the use of ASM rules to protect against Local File Inclusion (LFI) attacks.
func OpenFile(ctx context.Context, path string, flag int, perm os.FileMode) (file *os.File, err error) {
	return v2.OpenFile(ctx, path, flag, perm)
}
