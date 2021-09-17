package http

import (
	httpinstr "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/instrumentation/http"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/internal/protection/waf"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"
)

func Register() (ids []dyngo.EventListenerID) {
	return dyngo.Register(
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
