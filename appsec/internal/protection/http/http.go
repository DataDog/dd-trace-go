package http

import (
	"github.com/DataDog/dd-trace-go/appsec/dyngo"
	httpinstr "github.com/DataDog/dd-trace-go/appsec/instrumentation/http"
	"github.com/DataDog/dd-trace-go/appsec/internal/protection/waf"
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
