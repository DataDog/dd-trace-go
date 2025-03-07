package river

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

type config struct {
	serviceName    string
	insertSpanName string
	workSpanName   string
	measured       bool
	spanOpts       []tracer.StartSpanOption
}

func defaultConfig() *config {
	return &config{
		serviceName:    namingschema.ServiceNameOverrideV0("", ""),
		insertSpanName: "river.insert",
		workSpanName:   "river.work",
		measured:       false,
	}
}

// Option is used to customize spans started by InsertMiddleware or WorkerMiddleware.
type Option func(cfg *config)

// WithServiceName sets the service name tag for traces started by InsertMiddleware or WorkerMiddleware.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}

// WithMeasured sets the measured tag for traces started by InsertMiddleware or WorkerMiddleware.
func WithMeasured() Option {
	return func(cfg *config) {
		cfg.measured = true
	}
}
