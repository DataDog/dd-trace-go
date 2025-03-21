// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

func newCoverageData(n int) []*ciTestCoverageData {
	list := make([]*ciTestCoverageData, n)
	for i := 0; i < n; i++ {
		cov := newCiTestCoverageData(NewTestCoverage(uint64(i), uint64(i), uint64(i), uint64(i), "").(*testCoverage))
		list[i] = cov
	}

	return list
}

// TestCoveragePayloadIntegrity tests that whatever we push into the payload
// allows us to read the same content as would have been encoded by
// the codec.
func TestCoveragePayloadIntegrity(t *testing.T) {
	want := new(bytes.Buffer)
	for _, n := range []int{10, 1 << 10, 1 << 17} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newCoveragePayload()
			var allEvents ciTestCoverages

			for i := 0; i < n; i++ {
				list := newCoverageData(i%5 + 1)
				allEvents = append(allEvents, list...)
				for _, event := range list {
					p.push(event)
				}
			}

			want.Reset()
			err := msgp.Encode(want, allEvents)
			assert.NoError(err)
			assert.Equal(want.Len(), p.size())
			assert.Equal(p.itemCount(), len(allEvents))

			got, err := io.ReadAll(p)
			assert.NoError(err)
			assert.Equal(want.Bytes(), got)
		})
	}
}

// TestCoveragePayloadDecode ensures that whatever we push into the payload can
// be decoded by the codec.
func TestCoveragePayloadDecode(t *testing.T) {
	for _, n := range []int{10, 1 << 10} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			assert := assert.New(t)
			p := newCoveragePayload()
			for i := 0; i < n; i++ {
				list := newCoverageData(i%5 + 1)
				for _, event := range list {
					p.push(event)
				}
			}
			var got ciTestCoverages
			err := msgp.Decode(p, &got)
			assert.NoError(err)
		})
	}
}

func TestCoveragePayloadEnvelope(t *testing.T) {
	assert := assert.New(t)
	p := newCoveragePayload()
	encodedBuf, err := p.getBuffer()
	assert.NoError(err)

	// Convert the message pack to json
	jsonBuf := new(bytes.Buffer)
	_, err = msgp.CopyToJSON(jsonBuf, encodedBuf)
	assert.NoError(err)

	// Decode the json payload
	var testCyclePayload ciTestCovPayload
	err = json.Unmarshal(jsonBuf.Bytes(), &testCyclePayload)
	assert.NoError(err)

	// Now let's assert the decoded envelope metadata
	assert.Equal(testCyclePayload.Version, int32(2))
	assert.Empty(testCyclePayload.Coverages)
}
