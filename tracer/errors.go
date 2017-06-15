package tracer

import (
	"log"
	"strconv"
)

// errorTraceChanFull is raised when there's no more room in the channel
type errorTraceChanFull struct {
	// Len is the length of the channel (which is full)
	Len int
}

// Error provides a readable error message.
func (e *errorTraceChanFull) Error() string {
	return "trace channel is full (length: " + strconv.Itoa(e.Len) + ")"
}

// errorServiceChanFull is raised when there's no more room in the channel
type errorServiceChanFull struct {
	// Len is the length of the channel (which is full)
	Len int
}

// Error provides a readable error message.
func (e *errorServiceChanFull) Error() string {
	return "service channel is full (length: " + strconv.Itoa(e.Len) + ")"
}

// errorSpanBufFull is raised when there's no more room in the buffer
type errorSpanBufFull struct {
	// Len is the length of the buffer (which is full)
	Len int
}

// Error provides a readable error message.
func (e *errorSpanBufFull) Error() string {
	return "span buffer is full (length: " + strconv.Itoa(e.Len) + ")"
}

// errorTraceIDMismatch is raised when a trying to put a span in the wrong place.
type errorTraceIDMismatch struct {
	// Expected is the trace ID we should have.
	Expected uint64
	// Actual is the trace ID we have and is wrong.
	Actual uint64
}

// Error provides a readable error message.
func (e *errorTraceIDMismatch) Error() string {
	return "trace ID mismatch (expected: " +
		strconv.FormatUint(e.Expected, 16) +
		" actual: " +
		strconv.FormatUint(e.Actual, 16) +
		")"
}

// errorNoSpanBuf is raised when trying to finish/push a span that has no buffer associated to it.
type errorNoSpanBuf struct {
	// SpanName is the name of the span which could not be pushed (hint for the log reader).
	SpanName string
}

// Error provides a readable error message.
func (e *errorNoSpanBuf) Error() string {
	return "no span buffer (span name: '" + e.SpanName + "')"
}

type errorSummary struct {
	Count   int
	Example string
}

// errorKey returns a unique key for each error type
func errorKey(err error) string {
	switch err.(type) {
	case *errorTraceChanFull:
		return "ErrorTraceChanFull"
	case *errorServiceChanFull:
		return "ErrorServiceChanFull"
	case *errorSpanBufFull:
		return "ErrorSpanBufFull"
	case *errorTraceIDMismatch:
		return "ErrorTraceIDMismatch"
	case *errorNoSpanBuf:
		return "ErrorNoSpanBuf"
	}
	return "ErrorUnexpected"
}

func aggregateErrors(errChan <-chan error) map[string]errorSummary {
	errs := make(map[string]errorSummary, len(errChan))

	for {
		select {
		case err := <-errChan:
			if err != nil { // double-checking, we don't want to panic here...
				key := errorKey(err)
				summary := errs[key]
				summary.Count++
				summary.Example = err.Error()
				errs[key] = summary
			}
		default: // stop when there's no more data
			return errs
		}
	}
}

// logErrors logs the errors, preventing log file flooding, when there
// are many messages, it caps them and shows a quick summary.
// As of today it only logs using standard golang log package, but
// later we could send those stats to agent [TODO:christian].
func logErrors(errChan <-chan error) {
	errs := aggregateErrors(errChan)

	for k, v := range errs {
		if v.Count == 1 {
			log.Printf("%s: %s", k, v.Example)
		} else {
			log.Printf("%s: %s (repeated %d times)", k, v.Example, v.Count)
		}
	}
}
