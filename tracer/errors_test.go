package tracer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorTraceChanFull(t *testing.T) {
	assert := assert.New(t)

	err := &errorTraceChanFull{Len: 42}
	assert.Equal("trace channel is full (length: 42)", err.Error())
	assert.Equal("ErrorTraceChanFull", errorKey(err))
}

func TestErrorServiceChanFull(t *testing.T) {
	assert := assert.New(t)

	err := &errorServiceChanFull{Len: 42}
	assert.Equal("service channel is full (length: 42)", err.Error())
	assert.Equal("ErrorServiceChanFull", errorKey(err))
}

func TestErrorSpanBufFull(t *testing.T) {
	assert := assert.New(t)

	err := &errorSpanBufFull{Len: 42}
	assert.Equal("span buffer is full (length: 42)", err.Error())
	assert.Equal("ErrorSpanBufFull", errorKey(err))
}

func TestErrorTraceIDMismatch(t *testing.T) {
	assert := assert.New(t)

	err := &errorTraceIDMismatch{Expected: 42, Actual: 65535}
	assert.Equal("trace ID mismatch (expected: 2a actual: ffff)", err.Error())
	assert.Equal("ErrorTraceIDMismatch", errorKey(err))
}

func TestErrorKey(t *testing.T) {
	assert := assert.New(t)

	assert.Equal("ErrorUnexpected", errorKey(fmt.Errorf("this is something unexpected")))
	assert.Equal("ErrorUnexpected", errorKey(nil))
}

func TestAggregateErrors(t *testing.T) {
	assert := assert.New(t)

	errChan := make(chan error, 100)
	errChan <- &errorTraceChanFull{Len: 1000}
	errChan <- &errorTraceChanFull{Len: 1000}
	errChan <- &errorTraceChanFull{Len: 1000}
	errChan <- &errorTraceChanFull{Len: 1000}
	errChan <- &errorServiceChanFull{Len: 10}
	errChan <- &errorTraceIDMismatch{Expected: 42, Actual: 1}
	errChan <- &errorTraceIDMismatch{Expected: 42, Actual: 4095}

	errs := aggregateErrors(errChan)

	assert.Equal(map[string]errorSummary{
		"ErrorTraceChanFull": errorSummary{
			Count:   4,
			Example: "trace channel is full (length: 1000)",
		},
		"ErrorServiceChanFull": errorSummary{
			Count:   1,
			Example: "service channel is full (length: 10)",
		},
		"ErrorTraceIDMismatch": errorSummary{
			Count:   2,
			Example: "trace ID mismatch (expected: 2a actual: fff)",
		},
	}, errs)
}
