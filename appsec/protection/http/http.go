package http

import (
	"gopkg.in/DataDog/dd-trace-go.v1/appsec/dyngo"
	httpinstr "gopkg.in/DataDog/dd-trace-go.v1/appsec/instrumentation/http"
	"gopkg.in/DataDog/dd-trace-go.v1/appsec/waf"
)

func init() {
	dyngo.Register(
		dyngo.InstrumentationDescriptor{
			Title: "HTTP WAF Data Listener",
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: waf.NewOperationEventListener(),
			},
		},

		dyngo.InstrumentationDescriptor{
			Title: "HTTP Data Emitter",
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnStartEventListener(func(op *dyngo.Operation, args httpinstr.HandlerOperationArgs) {
					httpinstr.EmitData(op, args)
				}),
			},
		},
	)
}
