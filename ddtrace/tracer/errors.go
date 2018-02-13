package tracer

import (
	"fmt"
	"log"
	"strconv"
)

const errorPrefix = "Datadog Tracer Error: "

// errBufferFull is returned when a buffer is full. It describes the name
// of the buffer that was filled and it's size.
type errBufferFull struct {
	name string // buffer name
	size int    // buffer size
}

func (e *errBufferFull) Error() string {
	return fmt.Sprintf("%s is full (size: %d)", e.name, e.size)
}

// errLostData is returned when data such as traces or services was dropped.
// It contains information such as the name of the data that was lost, the count
// of items lost and optionally an error provided as context.
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

func aggregateErrors(errChan <-chan error) map[string]errorSummary {
	errs := make(map[string]errorSummary, len(errChan))
	for {
		select {
		case err := <-errChan:
			if err == nil {
				break
			}
			key := fmt.Sprintf("%T", err)
			summary := errs[key]
			summary.Count++
			summary.Example = err.Error()
			errs[key] = summary
		default: // stop when there's no more data
			return errs
		}
	}
}

// logErrors logs the errors, preventing log file flooding, when there
// are many messages, it caps them and shows a quick summary.
// As of today it only logs using standard golang log package, but
// later we could send those stats to agent // TODO(ufoot).
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
