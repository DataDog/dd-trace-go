package tracer

import "github.com/DataDog/dd-trace-go/v2/internal/log"

// Logger implementations are able to log given messages that the tracer or profiler might output.
type Logger interface {
	// Log prints the given message.
	Log(msg string)
}

// UseLogger sets l as the logger for all tracer and profiler logs.
func UseLogger(l Logger) {
	log.UseLogger(l)
}

// LogLevel represents the logging level that the log package prints at.
type LogLevel log.Level

func (l LogLevel) String() string {
	return log.Level(l).String()
}

type loggerAdapter struct {
	fn func(lvl LogLevel, msg string, a ...any)
}

func (l loggerAdapter) Log(msg string) {
	l.fn(LogLevel(log.CurrentLevel()), msg)
}

// AdaptLogger adapts a function to the Logger interface to adapt any logger
// to the Logger interface.
func AdaptLogger(fn func(lvl LogLevel, msg string, a ...any)) Logger {
	return loggerAdapter{
		fn: fn,
	}
}
