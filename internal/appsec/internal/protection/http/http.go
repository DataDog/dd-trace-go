// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	httpinstr "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/instrumentation/http"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
)

// Register the HTTP protections.
func Register() dyngo.UnregisterFunc {
	return dyngo.Register(
		dyngo.InstrumentationDescriptor{
			Title: "HTTP WAF Data Listener",
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: waf.NewOperationEventListener(),
			},
		},
		// TODO(julio): remove this temporary hack in milestone 1
		dyngo.InstrumentationDescriptor{
			Title: "HTTP Data Emitter",
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnStartEventListener((*httpinstr.HandlerOperationArgs)(nil), func(op *dyngo.Operation, v interface{}) {
					args := v.(httpinstr.HandlerOperationArgs)
					if len(args.Headers) > 0 {
						op.EmitData(args.Headers)
					}
					op.EmitData(args.UserAgent)
					if len(args.QueryValues) > 0 {
						op.EmitData(args.QueryValues)
					}
				}),
			},
		},
	)
}
