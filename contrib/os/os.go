// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// Package os provides integrations into the standard library's `os` package,
// allowing protection against Local File Inclusion (LFI) and Command Injection
// (CMDi) attacks.
package os

import (
	"context"
	"os"

	"github.com/DataDog/dd-trace-go/v2/contrib/os/internal/rasp"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageOS)
}

// OpenFile is a [context.Context]-aware version of [os.OpenFile], that allows
// the use of ASM rules to protect against Local File Inclusion (LFI) attacks.
func OpenFile(ctx context.Context, path string, flag int, perm os.FileMode) (file *os.File, err error) {
	if finish, block := rasp.StartOpen(ctx, path, flag, perm); finish != nil {
		defer finish(&file, &err)
		if block {
			return
		}
	}

	return os.OpenFile(path, flag, perm)
}

// StartProcess is a [context.Context]-aware version of [os.StartProcess], that allows
// the use of ASM rules to protect against Command Injection (CMDi) attacks.
func StartProcess(ctx context.Context, name string, argv []string, attr *os.ProcAttr) (proc *os.Process, err error) {
	if finish, block := rasp.StartProcess(ctx, name, argv); finish != nil {
		defer finish(&proc, &err)
		if block {
			return
		}
	}

	return os.StartProcess(name, argv, attr)
}
