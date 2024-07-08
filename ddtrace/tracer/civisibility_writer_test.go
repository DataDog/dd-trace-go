// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

func TestCIVisibilityImplementsTraceWriter(t *testing.T) {
	assert.Implements(t, (*traceWriter)(nil), &ciVisibilityTraceWriter{})
}

type failingCiVisibilityTransport struct {
	dummyTransport
	failCount    int
	sendAttempts int
	tracesSent   bool
	events       ciVisibilityEvents
	assert       *assert.Assertions
}

func (t *failingCiVisibilityTransport) send(p *payload) (io.ReadCloser, error) {
	t.sendAttempts++

	ciVisibilityPayload := &ciVisibilityPayload{p}

	var events ciVisibilityEvents
	err := msgp.Decode(ciVisibilityPayload, &events)
	if err != nil {
		return nil, err
	}
	if t.sendAttempts == 1 {
		t.events = events
	} else {
		t.assert.Equal(t.events, events)
	}

	if t.failCount > 0 {
		t.failCount--
		return nil, errors.New("oops, I failed")
	}

	t.tracesSent = true
	return io.NopCloser(strings.NewReader("OK")), nil
}

func TestCiVisibilityTraceWriterFlushRetries(t *testing.T) {
	testcases := []struct {
		configRetries int
		failCount     int
		tracesSent    bool
		expAttempts   int
	}{
		{configRetries: 0, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 0, failCount: 1, tracesSent: false, expAttempts: 1},

		{configRetries: 1, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 1, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 1, failCount: 2, tracesSent: false, expAttempts: 2},

		{configRetries: 2, failCount: 0, tracesSent: true, expAttempts: 1},
		{configRetries: 2, failCount: 1, tracesSent: true, expAttempts: 2},
		{configRetries: 2, failCount: 2, tracesSent: true, expAttempts: 3},
		{configRetries: 2, failCount: 3, tracesSent: false, expAttempts: 3},
	}

	ss := []*span{makeSpan(0)}
	for _, test := range testcases {
		name := fmt.Sprintf("%d-%d-%t-%d", test.configRetries, test.failCount, test.tracesSent, test.expAttempts)
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			p := &failingCiVisibilityTransport{
				failCount: test.failCount,
				assert:    assert,
			}
			c := newConfig(func(c *config) {
				c.transport = p
				c.sendRetries = test.configRetries
			})

			h := newCiVisibilityTraceWriter(c)
			h.add(ss)

			h.flush()
			h.wg.Wait()

			assert.Equal(test.expAttempts, p.sendAttempts)
			assert.Equal(test.tracesSent, p.tracesSent)
		})
	}
}
