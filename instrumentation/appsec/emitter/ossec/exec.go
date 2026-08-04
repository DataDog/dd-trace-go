// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package ossec

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
)

type (
	// RunCommandOperation type embodies any kind of function call that will result in a call to an execve(2) syscall
	RunCommandOperation struct {
		dyngo.Operation
	}

	// RunCommandOperationArgs is the arguments for a command execution operation
	RunCommandOperationArgs struct {
		// Name is the resolved executable path (os.StartProcess name); enforced as argv[0] for WAF evaluation.
		Name string
		// Commands is the argument vector (argv) passed to the execve(2) syscall
		Commands []string
	}

	// RunCommandOperationRes is the result of a command execution operation
	RunCommandOperationRes[Process any] struct {
		// Process is the process handle returned by the execution
		Process *Process
		// Err is the error returned by the function
		Err *error
	}
)

func (RunCommandOperationArgs) IsArgOf(*RunCommandOperation)            {}
func (RunCommandOperationRes[Process]) IsResultOf(*RunCommandOperation) {}
