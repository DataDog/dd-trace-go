package types

import httpinstr "github.com/DataDog/dd-trace-go/appsec/instrumentation/http"

type (
	SecurityEvent struct {
		Event   interface{}
		Context []SecurityEventContext
	}

	SecurityEventContext interface {
		isSecurityEventContext()
	}
)

func NewSecurityEvent(event interface{}, ctx ...SecurityEventContext) *SecurityEvent {
	return &SecurityEvent{
		Event:   event,
		Context: ctx,
	}
}

func (e *SecurityEvent) AddContext(ctx ...SecurityEventContext) {
	e.Context = append(e.Context, ctx...)
}

type (
	HTTPOperationContext struct {
		Request  HTTPRequestContext
		Response HTTPResponseContext
	}

	HTTPRequestContext struct {
		Method     string
		Host       string
		IsTLS      bool
		RequestURI string
		RemoteAddr string
	}

	HTTPResponseContext struct {
		Status int
	}
)

func WithHTTPOperationContext(args httpinstr.HandlerOperationArgs, res httpinstr.HandlerOperationRes) HTTPOperationContext {
	return HTTPOperationContext{
		Request: HTTPRequestContext{
			Method:     string(args.Method),
			Host:       string(args.Host),
			IsTLS:      args.IsTLS,
			RequestURI: string(args.RequestURI),
			RemoteAddr: string(args.RemoteAddr),
		},
		Response: HTTPResponseContext{
			Status: res.Status,
		},
	}
}

func (HTTPOperationContext) isSecurityEventContext() {}

type (
	SpanContext struct {
		TraceID, SpanID uint64
	}
)

func (SpanContext) isSecurityEventContext() {}

type ServiceContext struct {
	Name, Version, Environment string
}

func (ServiceContext) isSecurityEventContext() {}

type TagContext []string

func (TagContext) isSecurityEventContext() {}

type TracerContext struct {
	Runtime, RuntimeVersion, Version string
}

func (TracerContext) isSecurityEventContext() {}

type HostContext struct {
	Hostname, OS string
}

func (HostContext) isSecurityEventContext() {}
