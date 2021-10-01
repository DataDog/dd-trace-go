// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo

import "gopkg.in/DataDog/dd-trace-go.v1/internal/log"

type (
	// InstrumentationDescriptor is the high-level instrumentation descriptor entrypoint allowing describing what
	// to instrument with what instrumentation technique.
	InstrumentationDescriptor struct {
		Title           string
		Instrumentation Instrumentation
	}

	// Instrumentation descriptor interface of the instrumentation technique to use.
	Instrumentation interface{ isInstrumentation() }

	// OperationInstrumentation is the operation-based instrumentation descriptor. It allows instrumenting operations
	// using their event listeners.
	OperationInstrumentation struct {
		// EventListener to register to the root operation in order to enable this instrumentation. The root operation
		// is the operation entrypoint allowing to listen to every possible future events.
		EventListener EventListener
	}
)

func (OperationInstrumentation) isInstrumentation() {}

// Register is the instrumentation entrypoint allowing to register high-level instrumentation descriptors describing
// what to instrument.
// Only OperationInstrumentation is supported for now.
func Register(descriptors ...InstrumentationDescriptor) UnregisterFunc {
	type unregisterDesc struct {
		title      string
		unregister UnregisterFunc
	}
	var unregisterDescs []unregisterDesc
	for _, desc := range descriptors {
		switch actual := desc.Instrumentation.(type) {
		case OperationInstrumentation:
			listener := actual.EventListener
			if listener == nil {
				log.Debug("appsec: ignoring instrumentation %s", desc.Title)
				continue
			}
			log.Debug("appsec: registering instrumentation %s", desc.Title)
			unregisterDescs = append(unregisterDescs, unregisterDesc{title: desc.Title, unregister: root.Register(actual.EventListener)})
		}
	}
	return func() {
		for _, desc := range unregisterDescs {
			log.Debug("appsec: unregistering instrumentation %s", desc.title)
			desc.unregister()
		}
	}
}
