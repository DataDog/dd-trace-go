package tracer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAggregateErrors(t *testing.T) {
	assert := assert.New(t)

	errChan := make(chan error, 100)
	errChan <- &traceEncodingError{context: errors.New("couldn't encode at byte 0")}
	errChan <- &traceEncodingError{context: errors.New("couldn't encode at byte 0")}
	errChan <- &traceEncodingError{context: errors.New("couldn't encode at byte 0")}
	errChan <- &traceEncodingError{context: errors.New("couldn't encode at byte 0")}
	errChan <- &dataLossError{count: 42}
	errChan <- nil
	errChan <- errors.New("unexpected error type")

	errs := aggregateErrors(errChan)

	assert.Equal(map[string]errorSummary{
		"*tracer.traceEncodingError": errorSummary{
			Count:   4,
			Example: "error encoding trace: couldn't encode at byte 0",
		},
		"*tracer.dataLossError": errorSummary{
			Count:   1,
			Example: "lost traces (count: 42), error: <nil>",
		},
		"*errors.errorString": errorSummary{
			Count:   1,
			Example: "unexpected error type",
		},
	}, errs)
}
