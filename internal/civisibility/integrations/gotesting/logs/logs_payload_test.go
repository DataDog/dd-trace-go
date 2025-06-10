// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package logs

import (
	"bytes"
	"io"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

func newLogEntry(i int) *logEntry {
	return &logEntry{
		DdSource: "testoptimization",
		DdTags:   "",
		Hostname: "host",
		Message:  "My Message " + strconv.Itoa(i),
		Service:  "service",
	}
}

// TestLogsPayloadIntegrity tests that whatever we push into the payload
// allows us to read the same content as would have been encoded by
// the codec.
func TestLogsPayloadIntegrity(t *testing.T) {
	want := new(bytes.Buffer)
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newLogsPayload()
			var lists logsEntriesPayload
			for i := 0; i < n; i++ {
				val := newLogEntry(i%5 + 1)
				lists = append(lists, val)
				p.push(val)
			}
			want.Reset()
			err := msgp.Encode(want, lists)
			assert.NoError(err)
			assert.Equal(want.Len(), p.size())
			assert.Equal(p.itemCount(), n)

			got, err := io.ReadAll(p)
			assert.NoError(err)
			assert.Equal(want.Bytes(), got)
		})
	}
}

// TestLogsPayloadDecode ensures that whatever we push into the payload can
// be decoded by the codec.
func TestLogsPayloadDecode(t *testing.T) {
	for _, n := range []int{10, 1 << 10} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newLogsPayload()
			for i := 0; i < n; i++ {
				p.push(newLogEntry(i%5 + 1))
			}
			var got logsEntriesPayload
			err := msgp.Decode(p, &got)
			assert.NoError(err)
		})
	}
}
