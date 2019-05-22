// Package log provides logging utilities for the tracer.
package log

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

// Level specifies the logging level that the log package prints at.
type Level int

const (
	// LevelDebug represents debug level messages.
	LevelDebug Level = iota
	// LevelWarn represents warning and errors.
	LevelWarn
)

var prefixMsg = fmt.Sprintf("Datadog Tracer %s", version.Tag)

var (
	mu     sync.RWMutex   // guards below fields
	level                 = LevelWarn
	logger ddtrace.Logger = &defaultLogger{l: log.New(os.Stderr, "", log.LstdFlags)}
)

// UseLogger sets l as the active logger.
func UseLogger(l ddtrace.Logger) {
	mu.Lock()
	defer mu.Unlock()
	logger = l
}

// SetLevel sets the given lvl for logging.
func SetLevel(lvl Level) {
	mu.Lock()
	defer mu.Unlock()
	level = lvl
}

// Debug prints the given message if the level is LevelDebug.
func Debug(fmt string, a ...interface{}) {
	mu.RLock()
	lvl := level
	mu.RUnlock()
	if lvl != LevelDebug {
		return
	}
	printMsg("DEBUG", fmt, a...)
}

// Warn prints a warning message.
func Warn(fmt string, a ...interface{}) {
	printMsg("WARN", fmt, a...)
}

var (
	errmu   sync.RWMutex                // guards below fields
	erragg  = map[string]*errorReport{} // aggregated errors
	errrate time.Duration               // the rate at which errors are reported
	erron   bool                        // true if errors are being aggregated
)

func init() {
	errrate = time.Minute
	if v, ok := os.LookupEnv("DD_LOGGING_RATE"); ok {
		if sec, err := strconv.ParseUint(v, 10, 64); err != nil {
			Warn("Invalid value for DD_LOGGING_RATE: %v", err)
		} else {
			errrate = time.Duration(sec) * time.Second
		}
	}
}

type errorReport struct {
	err   error
	count uint64
}

// Error aggregates errors under the given key. The aggregated errors are printed
// once a minute or once every DD_LOGGING_RATE number of seconds.
func Error(key, format string, a ...interface{}) {
	if reachedLimit(key) {
		// avoid too much lock contention on spammy errors
		return
	}
	errmu.Lock()
	defer errmu.Unlock()
	report, ok := erragg[key]
	if !ok {
		erragg[key] = &errorReport{err: fmt.Errorf(format, a...)}
		report = erragg[key]
	}
	report.count++
	if !erron {
		erron = true
		time.AfterFunc(errrate, Flush)
	}
}

// defaultErrorLimit specifies the maximum number of errors gathered in a report.
const defaultErrorLimit = 50

// reachedLimit reports whether the maximum count has been reached for this key.
func reachedLimit(key string) bool {
	errmu.RLock()
	e, ok := erragg[key]
	errmu.RUnlock()
	return ok && e.count > defaultErrorLimit
}

// Flush flushes and resets all aggregated errors to the logger.
func Flush() {
	errmu.Lock()
	defer errmu.Unlock()
	for _, report := range erragg {
		msg := fmt.Sprintf("%v", report.err)
		if report.count > defaultErrorLimit {
			msg += fmt.Sprintf(", %d+ additional messages skipped", defaultErrorLimit)
		} else if report.count > 1 {
			msg += fmt.Sprintf(", %d additional messages skipped", report.count-1)
		}
		printMsg("ERROR", msg)
	}
	for k := range erragg {
		// compiler-optimized map-clearing post go1.11 (golang/go#20138)
		delete(erragg, k)
	}
	erron = false
}

func printMsg(lvl, format string, a ...interface{}) {
	msg := fmt.Sprintf("%s %s: %s\n", prefixMsg, lvl, fmt.Sprintf(format, a...))
	mu.RLock()
	logger.Log(msg)
	mu.RUnlock()
}

type defaultLogger struct{ l *log.Logger }

func (p *defaultLogger) Log(msg string) { p.l.Print(msg) }
