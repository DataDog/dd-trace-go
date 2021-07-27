package http

import "gopkg.in/DataDog/dd-trace-go.v1/appsec/dyngo"

func init() {
	dyngo.Register(dyngo.InstrumentationDescriptor{
		Title: "HTTP Data Emitter",
		Instrumentation: dyngo.OperationInstrumentation{
			EventListener: dyngo.OnStartEventListener(func(op *dyngo.Operation, args HandlerOperationArgs) {
				op.EmitData(args.Headers)
				op.EmitData(args.UserAgent)
			}),
		},
	})
}
