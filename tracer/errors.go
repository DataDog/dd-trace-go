package tracer

import (
	"fmt"
	"log"
	"strconv"
)

const errorPrefix = "Datadog Tracer Error: "

type errBufferFull struct {
	name string // buffer name
	size int    // buffer size
}

func (e *errBufferFull) Error() string {
	return fmt.Sprintf("%s is full (size: %d)", e.name, e.size)
}

type errLostData struct {
	name    string // data that was lost
	count   int    // number of items lost
	context error  // any context error, if available
}

func (e *errLostData) Error() string {
	return fmt.Sprintf("couldn't flush %s (count: %d), error: %v", e.name, e.count, e.context)
}

type errorSummary struct {
	Count   int
	Example string
}

// errorKey returns a unique key for each error type
func errorKey(err error) string {
	if err == nil {
		return ""
	}
	switch err.(type) {
	case *errBufferFull:
		return "ErrBufferFull"
	case *errLostData:
		return "ErrLostData"
	}
	return err.Error() // possibly high cardinality, but this is unexpected
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

	for _, v := range errs {
		var repeat string
		if v.Count > 1 {
			repeat = " (repeated " + strconv.Itoa(v.Count) + " times)"
		}
		log.Println(errorPrefix + v.Example + repeat)
	}
}
