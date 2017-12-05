package opentracing

const (
	// SpanType defines the Span type (web, db, cache)
	SpanType = "span.type"
	// ServiceName defines the Service name for this Span
	ServiceName = "service.name"
	// ResourceName defines the Resource name for the Span
	ResourceName = "resource.name"
	// ErrorMsg defines the error message
	ErrorMsg = "error.msg"
	// ErrorType defines the error class
	ErrorType = "error.type"
	// ErrorStack defines the stack for the given error or panic
	ErrorStack = "error.stack"
)
