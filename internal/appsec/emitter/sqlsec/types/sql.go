// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package types

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

type (
	SQLOperation struct {
		dyngo.Operation
	}

	SQLOperationArgs struct {
		// Query corresponds to the addres `server.db.statement`
		Query string
		// Driver corresponds to the addres `server.db.system`
		Driver string
	}
	SQLOperationRes struct{}
)

func (SQLOperationArgs) IsArgOf(*SQLOperation)   {}
func (SQLOperationRes) IsResultOf(*SQLOperation) {}
