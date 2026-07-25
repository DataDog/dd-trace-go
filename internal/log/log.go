// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package log provides logging utilities for the tracer.
package log

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// Level specifies the logging level that the log package prints at.
type Level int

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

const (
	// LevelDebug represents debug level messages.
	LevelDebug Level = iota
	// LevelInfo represents informational messages.
	LevelInfo
	// LevelWarn represents warning messages.
	LevelWarn
	// LevelError represents error messages.
	LevelError
)

var prefixMsg = "Datadog Tracer " + version.Tag

// Logger implementations are able to log given messages that the tracer might
// output. This interface is duplicated here to avoid a cyclic dependency
// between this package and ddtrace
type Logger interface {
	// Log prints the given message.
	Log(msg string)
}

// File name for writing tracer logs, if DD_TRACE_LOG_DIRECTORY has been configured
const LoggerFile = "ddtrace.log"

// ManagedFile functions like a *os.File but is safe for concurrent use
type ManagedFile struct {
	mu     sync.RWMutex
	file   *os.File
	closed bool
}

// Close closes the ManagedFile's *os.File in a concurrent-safe manner, ensuring the file is closed only once
func (m *ManagedFile) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil || m.closed {
		return nil
	}
	err := m.file.Close()
	if err != nil {
		return err
	}
	m.closed = true
	return nil
}

func (m *ManagedFile) Name() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.file == nil {
		return ""
	}
	return m.file.Name()
}

var (
	levelThreshold atomic.Int32 // stores Level as int32; accessed atomically to avoid lock contention in hot paths
	mu             sync.Mutex   // guards the logger slot and its callback leases
	logger         Logger       = &defaultLogger{l: log.New(os.Stderr, "", log.LstdFlags)}
	activeSlot                  = newLoggerSlot(logger, logger, false)
)

type loggerSlot struct {
	logger   Logger
	fallback Logger
	leased   bool

	refs            uint64
	retired         bool
	retireRequested bool
	finished        bool
	finalizers      []func()
}

func newLoggerSlot(logger, fallback Logger, leased bool) *loggerSlot {
	return &loggerSlot{
		logger:   logger,
		fallback: fallback,
		leased:   leased,
	}
}

// LoggerLease owns an installed logger slot. Retire prevents new callbacks
// from acquiring the slot and runs its finalizer after callbacks which already
// acquired it return.
type LoggerLease struct {
	slot *loggerSlot
	once sync.Once
}

func init() {
	levelThreshold.Store(int32(LevelWarn))
}

// UseLogger sets l as the active logger and returns a function to restore the
// previous logger. The return value is mostly useful when testing.
func UseLogger(l Logger) (undo func()) {
	Flush()
	return InstallLogger(l)
}

// InstallLogger sets l as the active logger without flushing pending errors.
// Callers that need the legacy flush behavior should use UseLogger.
func InstallLogger(l Logger) (undo func()) {
	old := installLogger(l, l, false)
	var once sync.Once
	return func() {
		once.Do(func() {
			restoreLogger(old)
		})
	}
}

// InstallLoggerWithLease installs a process-generation logger. A nil logger
// inherits the logger which preceded the current process generation, so a
// replacement cannot leave a retired file logger in the global slot.
//
// Installing a logger only retires the previous slot; it never waits for an
// in-flight callback. Call Retire when the installed logger's backing resource
// is ready to be released.
func InstallLoggerWithLease(l Logger) *LoggerLease {
	mu.Lock()
	fallback := activeSlot.logger
	if activeSlot.leased {
		fallback = activeSlot.fallback
	}
	if l == nil {
		l = fallback
	}
	slot := newLoggerSlot(l, fallback, true)
	finished := swapLoggerSlotLocked(slot)
	mu.Unlock()
	runLoggerFinalizers(finished)
	return &LoggerLease{slot: slot}
}

// Retire removes the leased logger from the active slot if necessary. The
// optional finalizer runs after every callback which acquired the logger has
// returned. Retire itself does not wait for those callbacks.
func (l *LoggerLease) Retire(finalizer func()) {
	if l == nil {
		if finalizer != nil {
			finalizer()
		}
		return
	}
	l.once.Do(func() {
		mu.Lock()
		var finished []*loggerSlot
		if activeSlot == l.slot {
			fallback := l.slot.fallback
			replacement := newLoggerSlot(fallback, fallback, false)
			finished = append(finished, swapLoggerSlotLocked(replacement)...)
		}
		if finalizer != nil {
			l.slot.finalizers = append(l.slot.finalizers, finalizer)
		}
		l.slot.retireRequested = true
		if completed := finishLoggerSlotLocked(l.slot); completed != nil {
			finished = append(finished, completed)
		}
		mu.Unlock()
		runLoggerFinalizers(finished)
	})
}

func installLogger(l, fallback Logger, leased bool) *loggerSlot {
	mu.Lock()
	old := activeSlot
	finished := swapLoggerSlotLocked(newLoggerSlot(l, fallback, leased))
	mu.Unlock()
	runLoggerFinalizers(finished)
	return old
}

func restoreLogger(old *loggerSlot) {
	mu.Lock()
	var next *loggerSlot
	if old.leased && !old.retireRequested && !old.finished {
		old.retired = false
		next = old
	} else {
		restored := old.logger
		if old.leased {
			restored = old.fallback
		}
		next = newLoggerSlot(restored, restored, false)
	}
	var finished []*loggerSlot
	if activeSlot != next {
		finished = swapLoggerSlotLocked(next)
	}
	mu.Unlock()
	runLoggerFinalizers(finished)
}

func swapLoggerSlotLocked(next *loggerSlot) []*loggerSlot {
	old := activeSlot
	activeSlot = next
	logger = next.logger
	old.retired = true
	if !old.leased {
		old.retireRequested = true
	}
	if completed := finishLoggerSlotLocked(old); completed != nil {
		return []*loggerSlot{completed}
	}
	return nil
}

func finishLoggerSlotLocked(slot *loggerSlot) *loggerSlot {
	if slot.finished || !slot.retired || !slot.retireRequested || slot.refs != 0 {
		return nil
	}
	slot.finished = true
	return slot
}

func runLoggerFinalizers(slots []*loggerSlot) {
	for _, slot := range slots {
		for _, finalizer := range slot.finalizers {
			finalizer()
		}
	}
}

// OpenFileAtPath creates a new file at the specified dirPath and configures the logger to write to this file. The dirPath must already exist on the underlying os.
// It returns the file that was created, or nil and an error if the file creation was unsuccessful.
// The caller of OpenFileAtPath is responsible for calling Close() on the ManagedFile
func OpenFileAtPath(dirPath string) (*ManagedFile, error) {
	managed, fileLogger, err := PrepareFileAtPath(dirPath)
	if err != nil {
		return nil, err
	}
	UseLogger(fileLogger)
	return managed, nil
}

// PrepareFileAtPath opens the configured tracer log file without installing
// it as the process logger. Callers can perform the filesystem I/O before a
// generation bridge and install the returned Logger only after validating the
// publication.
func PrepareFileAtPath(dirPath string) (*ManagedFile, Logger, error) {
	path, err := os.Stat(dirPath)
	if err != nil || !path.IsDir() {
		return nil, nil, fmt.Errorf("file path %v invalid or does not exist on the underlying os; using default logger to stderr", dirPath)
	}
	filepath := dirPath + "/" + LoggerFile
	f, err := os.OpenFile(filepath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, nil, fmt.Errorf("using default logger to stderr due to error creating or opening log file: %s", err.Error())
	}
	managed := &ManagedFile{
		file: f,
	}
	return managed, &defaultLogger{l: log.New(f, "", log.LstdFlags)}, nil
}

// SetLevel sets the given lvl as log threshold for logging.
func SetLevel(lvl Level) {
	levelThreshold.Store(int32(lvl))
}

func DefaultLevel() Level {
	return GetLevel()
}

// GetLevel returns the currrent log level.
func GetLevel() Level {
	return Level(levelThreshold.Load())
}

// DebugEnabled returns true if debug log messages are enabled. This can be used in extremely
// hot code paths to avoid allocating the ...interface{} argument.
func DebugEnabled() bool {
	return GetLevel() == LevelDebug
}

// Debug prints the given message if the level is LevelDebug.
func Debug(fmt string, a ...any) {
	if !DebugEnabled() {
		return
	}
	printMsg(LevelDebug, fmt, a...)
}

// Warn prints a warning message.
func Warn(fmt string, a ...any) {
	printMsg(LevelWarn, fmt, a...)
}

// Info prints an informational message.
func Info(fmt string, a ...any) {
	printMsg(LevelInfo, fmt, a...)
}

var (
	errmu   sync.RWMutex                // guards below fields
	erragg  = map[string]*errorReport{} // aggregated errors
	errrate = time.Minute               // the rate at which errors are reported
	erron   bool                        // true if errors are being aggregated
)

func init() {
	// This is required because we really want to be able to log errors from dyngo
	// but the log package depend on too much packages that we want to instrument.
	// So we need to do this to avoid dependency cycles.
	dyngo.LogError = Error
}

// SetLoggingRate applies an already-resolved DD_LOGGING_RATE value.
func SetLoggingRate(v string) {
	setLoggingRate(v)
}

func setLoggingRate(v string) {
	if sec, err := strconv.ParseInt(v, 10, 64); err != nil {
		Warn("Invalid value for DD_LOGGING_RATE: %s", err.Error())
	} else {
		if sec < 0 {
			Warn("Invalid value for DD_LOGGING_RATE: negative value")
		} else {
			// DD_LOGGING_RATE = 0 allows to log errors immediately.
			errrate = time.Duration(sec) * time.Second
		}
	}
}

type errorReport struct {
	first time.Time // time when first error occurred
	err   error
	count uint64
}

// Error reports an error. Errors get aggregated and logged periodically. The
// default is once per minute or once every DD_LOGGING_RATE number of seconds.
func Error(format string, a ...any) {
	key := format // format should 99.9% of the time be constant
	if reachedLimit(key) {
		// avoid too much lock contention on spammy errors
		return
	}
	errmu.Lock()
	defer errmu.Unlock()
	report, ok := erragg[key]
	if !ok {
		erragg[key] = &errorReport{
			err:   fmt.Errorf(format, a...),
			first: time.Now(),
		}
		report = erragg[key]
	}
	report.count++
	if errrate == 0 {
		flushLocked()
		return
	}
	if !erron {
		erron = true
		time.AfterFunc(errrate, Flush)
	}
}

// defaultErrorLimit specifies the maximum number of errors gathered in a report.
const defaultErrorLimit = 200

// reachedLimit reports whether the maximum count has been reached for this key.
func reachedLimit(key string) bool {
	errmu.RLock()
	e, ok := erragg[key]
	confirm := ok && e.count > defaultErrorLimit
	errmu.RUnlock()
	return confirm
}

// Flush flushes and resets all aggregated errors to the logger.
func Flush() {
	errmu.Lock()
	defer errmu.Unlock()
	flushLocked()
}

func flushLocked() {
	for _, report := range erragg {
		var extra string
		if report.count > defaultErrorLimit {
			extra = fmt.Sprintf(", %d+ additional messages skipped (first occurrence: %s)", defaultErrorLimit, report.first.Format(time.RFC822))
		} else if report.count > 1 {
			extra = fmt.Sprintf(", %d additional messages skipped (first occurrence: %s)", report.count-1, report.first.Format(time.RFC822))
		} else {
			extra = fmt.Sprintf(" (occurred: %s)", report.first.Format(time.RFC822))
		}
		printMsg(LevelError, "%v%s", report.err, extra)
	}
	for k := range erragg {
		// compiler-optimized map-clearing post go1.11 (golang/go#20138)
		delete(erragg, k)
	}
	erron = false
}

func printMsg(lvl Level, format string, a ...any) {
	var b strings.Builder
	b.Grow(len(prefixMsg) + 1 + len(lvl.String()) + 2 + len(format))
	b.WriteString(prefixMsg)
	b.WriteString(" ")
	b.WriteString(lvl.String())
	b.WriteString(": ")
	b.WriteString(fmt.Sprintf(format, a...))
	slot, current := acquireLogger()
	defer releaseLogger(slot)
	if ll, ok := current.(interface {
		LogL(lvl Level, msg string)
	}); !ok {
		current.Log(b.String())
	} else {
		ll.LogL(lvl, b.String())
	}
}

func acquireLogger() (*loggerSlot, Logger) {
	mu.Lock()
	slot := activeSlot
	slot.refs++
	current := slot.logger
	mu.Unlock()
	return slot, current
}

func releaseLogger(slot *loggerSlot) {
	mu.Lock()
	slot.refs--
	finished := finishLoggerSlotLocked(slot)
	mu.Unlock()
	if finished != nil {
		runLoggerFinalizers([]*loggerSlot{finished})
	}
}

type defaultLogger struct{ l *log.Logger }

var _ Logger = &defaultLogger{}

func (p *defaultLogger) Log(msg string) { p.l.Print(msg) }

// DiscardLogger discards every call to Log().
type DiscardLogger struct{}

var _ Logger = &DiscardLogger{}

// Log implements Logger.
func (d DiscardLogger) Log(_ string) {}

// RecordLogger records every call to Log() and makes it available via Logs().
type RecordLogger struct {
	m      sync.Mutex
	logs   []string
	ignore []string // a log is ignored if it contains a string in ignored
}

var _ Logger = &RecordLogger{}

// Ignore adds substrings to the ignore field of RecordLogger, allowing
// the RecordLogger to ignore attempts to log strings with certain substrings.
func (r *RecordLogger) Ignore(substrings ...string) {
	r.m.Lock()
	defer r.m.Unlock()
	r.ignore = append(r.ignore, substrings...)
}

// Log implements Logger.
func (r *RecordLogger) Log(msg string) {
	r.m.Lock()
	defer r.m.Unlock()
	for _, ignored := range r.ignore {
		if strings.Contains(msg, ignored) {
			return
		}
	}
	r.logs = append(r.logs, msg)
}

// Logs returns the ordered list of logs recorded by the logger.
func (r *RecordLogger) Logs() []string {
	r.m.Lock()
	defer r.m.Unlock()
	copied := make([]string, len(r.logs))
	copy(copied, r.logs)
	return copied
}

// Reset resets the logger's internal logs
func (r *RecordLogger) Reset() {
	r.m.Lock()
	defer r.m.Unlock()
	r.logs = r.logs[:0]
	r.ignore = r.ignore[:0]
}
