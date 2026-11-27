// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules and hooks for the standard library os
// package. It is the otelc counterpart of ../orchestrion.yml.
package otelc

import (
	"context"
	"os"
	_ "unsafe" // for go:linkname

	"go.opentelemetry.io/otelc/pkg/hook"

	// Links in ../internal/rasp, which the declarations below point at.
	_ "github.com/DataDog/dd-trace-go/v2/contrib/os"
)

type (
	finishOpen  = func(file **os.File, err *error)
	finishStart = func(proc **os.Process, err *error)
)

// The hook module cannot import ../internal/rasp, so it links to it.
//
//go:linkname startOpen github.com/DataDog/dd-trace-go/v2/contrib/os/internal/rasp.StartOpen
func startOpen(ctx context.Context, path string, flag int, perm os.FileMode) (finishOpen, bool)

//go:linkname startProcess github.com/DataDog/dd-trace-go/v2/contrib/os/internal/rasp.StartProcess
func startProcess(ctx context.Context, name string, argv []string) (finishStart, bool)

// Result indices of os.OpenFile, (*os.Root).OpenFile and os.StartProcess.
const (
	valueResult = 0
	errorResult = 1
)

// BeforeOpenFile starts the LFI check for os.OpenFile. The operation comes
// from the goroutine's AppSec state, so the context carries nothing.
func BeforeOpenFile(ctx hook.HookContext, name string, flag int, perm os.FileMode) {
	beforeOpen(ctx, name, flag, perm)
}

// BeforeRootOpenFile starts the LFI check for (*os.Root).OpenFile.
func BeforeRootOpenFile(ctx hook.HookContext, _ *os.Root, name string, flag int, perm os.FileMode) {
	beforeOpen(ctx, name, flag, perm)
}

func beforeOpen(ctx hook.HookContext, name string, flag int, perm os.FileMode) {
	finish, block := startOpen(context.Background(), name, flag, perm)
	if finish == nil {
		return
	}
	ctx.SetData(finish)
	ctx.SetSkipCall(block)
}

// AfterOpenFile finishes the LFI check. When the check blocked the call, the
// results are zero and the listener sets the blocking error.
func AfterOpenFile(ctx hook.HookContext, file *os.File, err error) {
	finish, ok := ctx.GetData().(finishOpen)
	if !ok {
		return
	}
	finish(&file, &err)
	ctx.SetReturnVal(valueResult, file)
	ctx.SetReturnVal(errorResult, err)
}

// BeforeStartProcess starts the CMDi check for os.StartProcess, which
// os/exec also calls.
func BeforeStartProcess(ctx hook.HookContext, name string, argv []string, _ *os.ProcAttr) {
	finish, block := startProcess(context.Background(), name, argv)
	if finish == nil {
		return
	}
	ctx.SetData(finish)
	ctx.SetSkipCall(block)
}

// AfterStartProcess finishes the CMDi check. When the check blocked the call,
// the results are zero and the listener sets the blocking error.
func AfterStartProcess(ctx hook.HookContext, proc *os.Process, err error) {
	finish, ok := ctx.GetData().(finishStart)
	if !ok {
		return
	}
	finish(&proc, &err)
	ctx.SetReturnVal(valueResult, proc)
	ctx.SetReturnVal(errorResult, err)
}
