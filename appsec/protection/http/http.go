package http

import (
	"gopkg.in/DataDog/dd-trace-go.v1/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/appsec/waf"
)

func init() {
	dyngo.Register(dyngo.InstrumentationDescriptor{
		Title: "HTTP WAF",
		Instrumentation: dyngo.OperationInstrumentation{
			EventListener: waf.NewOperationEventListener(),
		},
	})
}
