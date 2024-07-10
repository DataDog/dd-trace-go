// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package types

import "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"

type (
	// OpenOperation type embodies any kind of function calls that will result in a call to an open(2) syscall
	OpenOperation struct {
		dyngo.Operation
	}

	// OpenOperationArgs is the arguments for an open operation
	OpenOperationArgs struct {
		// Path is the path to the file to be opened
		Path string
	}

	// OpenOperationRes is the result of an open operation
	OpenOperationRes struct{}
)

func (OpenOperationArgs) IsArgOf(*OpenOperation)   {}
func (OpenOperationRes) IsResultOf(*OpenOperation) {}
