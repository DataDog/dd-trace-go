// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo

type (
	InstrumentationDescriptor struct {
		Title           string
		Instrumentation Instrumentation
	}

	Instrumentation interface{ isInstrumentation() }

	OperationInstrumentation struct {
		EventListener EventListener
	}

	FunctionInstrumentation struct {
		Symbol   string
		Prologue interface{}
	}
)

func (OperationInstrumentation) isInstrumentation() {}
func (FunctionInstrumentation) isInstrumentation()  {}

// Register is the instrumentation entrypoint allowing to register high-level instrumentation descriptors describing
// what to instrument.
// Only OperationInstrumentation is supported for now.
func Register(descriptors ...InstrumentationDescriptor) UnregisterFunc {
	var unregisterFuncs []UnregisterFunc
	for _, desc := range descriptors {
		switch actual := desc.Instrumentation.(type) {
		case OperationInstrumentation:
			unregisterFuncs = append(unregisterFuncs, root.Register(actual.EventListener))
		}
	}
	return func() {
		for _, unregister := range unregisterFuncs {
			unregister()
		}
	}
}
