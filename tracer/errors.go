package tracer

import (
	"log"
	"strconv"
)

// ErrorTraceChanFull is raised when there's no more room in the channel
type ErrorTraceChanFull struct {
	// Len is the length of the channel (which is full)
	Len int
}

// Error provides a readable error message.
func (e *ErrorTraceChanFull) Error() string {
	return "trace channel is full (length: " + strconv.Itoa(e.Len) + ")"
}

// ErrorServiceChanFull is raised when there's no more room in the channel
type ErrorServiceChanFull struct {
	// Len is the length of the channel (which is full)
	Len int
}

// Error provides a readable error message.
func (e *ErrorServiceChanFull) Error() string {
	return "service channel is full (length: " + strconv.Itoa(e.Len) + ")"
}

// ErrorSpanBufFull is raised when there's no more room in the buffer
type ErrorSpanBufFull struct {
	// Len is the length of the buffer (which is full)
	Len int
}

// Error provides a readable error message.
func (e *ErrorSpanBufFull) Error() string {
	return "span buffer is full (length: " + strconv.Itoa(e.Len) + ")"
}

// ErrorTraceIDMismatch is raised when a trying to put a span in the wrong place.
type ErrorTraceIDMismatch struct {
	// Expected is the trace ID we should have.
	Expected uint64
	// Actual is the trace ID we have and is wrong.
	Actual uint64
}

// Error provides a readable error message.
func (e *ErrorTraceIDMismatch) Error() string {
	return "trace ID mismatch (expected: " +
		strconv.FormatUint(e.Expected, 16) +
		" actual: " +
		strconv.FormatUint(e.Actual, 16) +
		")"
}

type errorSummary struct {
	Count   int
	Example string
}

// getErrorKey returns a unique key for each error type
func getErrorKey(err error) string {
	switch err.(type) {
	case *ErrorTraceChanFull:
		return "ErrorTraceChanFull"
	case *ErrorServiceChanFull:
		return "ErrorServiceChanFull"
	case *ErrorSpanBufFull:
		return "ErrorSpanBufFull"
	case *ErrorTraceIDMismatch:
		return "ErrorTraceIDMismatch"
	}
	return "ErrorUnexpected"
}

func aggregateErrors(errs map[string]errorSummary, errChan <-chan error) {
	for {
		select {
		case err := <-errChan:
			key := getErrorKey(err)
			summary := errs[key]
			summary.Count++
			summary.Example = err.Error()
			errs[key] = summary
		default: // stop when there's no more data
			return
		}
	}
}

// logErrors logs the erros, preventing log file flooding, when there
// are many messages, it caps them and shows a quick summary
func logErrors(errChan <-chan error) {
	errs := make(map[string]errorSummary, len(errChan))

	aggregateErrors(errs, errChan)

	for k, v := range errs {
		if v.Count == 1 {
			log.Printf("%s: %s", k, v.Example)
		} else {
			log.Printf("%s: %s (repeated %d times)", k, v.Example, v.Count)
		}
	}
}
