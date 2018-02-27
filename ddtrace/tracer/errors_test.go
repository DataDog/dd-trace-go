package tracer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAggregateErrors(t *testing.T) {
	assert := assert.New(t)

	errChan := make(chan error, 100)
	errChan <- &errBufferFull{name: "span buffer", size: 1000}
	errChan <- &errBufferFull{name: "span buffer", size: 1000}
	errChan <- &errBufferFull{name: "span buffer", size: 1000}
	errChan <- &errBufferFull{name: "span buffer", size: 1000}
	errChan <- &errLostData{name: "traces", count: 42}
	errChan <- nil
	errChan <- errors.New("unexpected error type")

	errs := aggregateErrors(errChan)

	assert.Equal(map[string]errorSummary{
		"*tracer.errBufferFull": errorSummary{
			Count:   4,
			Example: "span buffer is full (size: 1000)",
		},
		"*tracer.errLostData": errorSummary{
			Count:   1,
			Example: "couldn't flush traces (count: 42), error: <nil>",
		},
		"*errors.errorString": errorSummary{
			Count:   1,
			Example: "unexpected error type",
		},
	}, errs)
}
