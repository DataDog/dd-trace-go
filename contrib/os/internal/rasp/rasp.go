// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package rasp runs the AppSec checks for opening files (LFI) and starting
// processes (CMDi). The contrib functions and the otelc hooks share it.
package rasp

import (
	"context"
	"os"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/ossec"
)

// FinishOpen finishes an LFI check with the results of the open call. A
// blocking listener replaces *err with the blocking error.
type FinishOpen = func(file **os.File, err *error)

// FinishStart finishes a CMDi check with the results of the start call. A
// blocking listener replaces *err with the blocking error.
type FinishStart = func(proc **os.Process, err *error)

// StartOpen starts the LFI check for opening path. It returns a nil finish
// when ctx holds no AppSec operation. When block is true, the caller must not
// open the file, and must call finish with its zero results.
func StartOpen(ctx context.Context, path string, flag int, perm os.FileMode) (finish FinishOpen, block bool) {
	parent, _ := dyngo.FromContext(ctx)
	if parent == nil {
		return nil, false
	}

	op := &ossec.OpenOperation{
		Operation: dyngo.NewOperation(parent),
	}
	dyngo.OnData(op, func(*events.BlockingSecurityEvent) {
		block = true
	})
	dyngo.StartOperation(op, ossec.OpenOperationArgs{
		Path:  path,
		Flags: flag,
		Perms: perm,
	})

	return func(file **os.File, err *error) {
		dyngo.FinishOperation(op, ossec.OpenOperationRes[*os.File]{
			File: file,
			Err:  err,
		})
	}, block
}

// StartProcess starts the CMDi check for running name with argv. It returns a
// nil finish when ctx holds no AppSec operation. When block is true, the
// caller must not start the process, and must call finish with its zero
// results.
func StartProcess(ctx context.Context, name string, argv []string) (finish FinishStart, block bool) {
	parent, _ := dyngo.FromContext(ctx)
	if parent == nil {
		return nil, false
	}

	op := &ossec.RunCommandOperation{
		Operation: dyngo.NewOperation(parent),
	}
	dyngo.OnData(op, func(*events.BlockingSecurityEvent) {
		block = true
	})
	dyngo.StartOperation(op, ossec.RunCommandOperationArgs{
		Name:     name,
		Commands: argv,
	})

	return func(proc **os.Process, err *error) {
		dyngo.FinishOperation(op, ossec.RunCommandOperationRes[*os.Process]{
			Process: proc,
			Err:     err,
		})
	}, block
}
