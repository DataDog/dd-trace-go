// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const debug = false

const (
	elasticV6URL = "http://127.0.0.1:9202"
	elasticV7URL = "http://127.0.0.1:9203"
	elasticV8URL = "http://127.0.0.1:9204"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestQuantize(t *testing.T) {
	for _, tc := range []struct {
		url, method string
		expected    string
	}{
		{
			url:      "/twitter/tweets",
			method:   "POST",
			expected: "POST /twitter/tweets",
		},
		{
			url:      "/logs_2016_05/event/_search",
			method:   "GET",
			expected: "GET /logs_?_?/event/_search",
		},
		{
			url:      "/twitter/tweets/123",
			method:   "GET",
			expected: "GET /twitter/tweets/?",
		},
		{
			url:      "/logs_2016_05/event/123",
			method:   "PUT",
			expected: "PUT /logs_?_?/event/?",
		},
	} {
		assert.Equal(t, tc.expected, quantize(tc.url, tc.method))
	}
}

func TestPeek(t *testing.T) {
	assert := assert.New(t)

	for _, tt := range [...]struct {
		max  int    // content length
		txt  string // stream
		n    int    // bytes to peek at
		snip string // expected snippet
		err  error  // expected error
	}{
		0: {
			// extract 3 bytes from a content of length 7
			txt:  "ABCDEFG",
			max:  7,
			n:    3,
			snip: "ABC",
		},
		1: {
			// extract 7 bytes from a content of length 7
			txt:  "ABCDEFG",
			max:  7,
			n:    7,
			snip: "ABCDEFG",
		},
		2: {
			// extract 100 bytes from a content of length 9 (impossible scenario)
			txt:  "ABCDEFG",
			max:  9,
			n:    100,
			snip: "ABCDEFG",
		},
		3: {
			// extract 5 bytes from a content of length 2 (impossible scenario)
			txt:  "ABCDEFG",
			max:  2,
			n:    5,
			snip: "AB",
		},
		4: {
			txt:  "ABCDEFG",
			max:  0,
			n:    1,
			snip: "A",
		},
		5: {
			n:   4,
			max: 4,
			err: errors.New("empty stream"),
		},
		6: {
			txt:  "ABCDEFG",
			n:    4,
			max:  -1,
			snip: "ABCD",
		},
	} {
		var readcloser io.ReadCloser
		if tt.txt != "" {
			readcloser = io.NopCloser(bytes.NewBufferString(tt.txt))
		}
		snip, rc, err := peek(readcloser, "", tt.max, tt.n)
		assert.Equal(tt.err, err)
		assert.Equal(tt.snip, snip)

		if readcloser != nil {
			// if a non-nil io.ReadCloser was sent, the returned io.ReadCloser
			// must always return the entire original content.
			all, err := io.ReadAll(rc)
			assert.Nil(err)
			assert.Equal(tt.txt, string(all))
		}
	}
}
