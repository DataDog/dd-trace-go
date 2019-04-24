package tracer

// Logger is the interface that wraps the necessary functionality for logging of errors messages.
//
// It uses subset of methods from log.Logger - therefore log.Logger will always fullfil
// requirements of interface.
type Logger interface {
	Println(v ...interface{})
	Printf(format string, v ...interface{})
}
