package tracer

import (
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

	errs := aggregateErrors(errChan)

	assert.Equal(map[string]errorSummary{
		"ErrBufferFull": errorSummary{
			Count:   4,
			Example: "span buffer is full (size: 1000)",
		},
		"ErrLostData": errorSummary{
			Count:   1,
			Example: "couldn't flush traces (count: 42), error: <nil>",
		},
	}, errs)
}
