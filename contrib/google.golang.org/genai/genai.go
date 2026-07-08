// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package genaitrace provides Datadog LLM Observability tracing for the
// Google GenAI Go SDK (google.golang.org/genai).
//
// Wrap a *genai.Client with [WrapClient] and use the returned *Client in
// place of the original; calls on its Models and Chats fields produce
// LLM Observability spans automatically.
package genaitrace // import "github.com/DataDog/dd-trace-go/contrib/google.golang.org/genai/v2"

import (
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGoogleAPIsGoGenAI)
}
