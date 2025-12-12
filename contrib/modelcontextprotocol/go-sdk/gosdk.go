// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk // import "github.com/DataDog/dd-trace-go/contrib/modelcontextprotocol/go-sdk"

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageModelContextProtocolGoSDK)
}
