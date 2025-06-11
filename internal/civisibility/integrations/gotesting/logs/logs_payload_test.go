// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package logs

import (
	"encoding/json"
	"io"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
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
			b, err := io.ReadAll(p)
			err = json.Unmarshal(b, &got)
			assert.NoError(err)
		})
	}
}
