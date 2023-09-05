// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeTransport struct {
	requests []*http.Request
}

func (t *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, r)
	return &http.Response{StatusCode: 200}, nil
}

func TestHTTPTransport(t *testing.T) {
	p := StatsPayload{Env: "env-1", Stats: []StatsBucket{{
		Start:    2,
		Duration: 10,
		Stats: []StatsPoint{{
			Service:        "service-1",
			EdgeTags:       []string{"edge-1"},
			Hash:           1,
			ParentHash:     2,
			PathwayLatency: []byte{1, 2, 3},
			EdgeLatency:    []byte{4, 5, 6},
		}},
	}}}
	fakeTransport := fakeTransport{}
	transport := newHTTPTransport(&url.URL{Scheme: "http", Host: "agent-address:8126"}, &http.Client{Transport: &fakeTransport})
	assert.Nil(t, transport.sendPipelineStats(&p))
	assert.Len(t, fakeTransport.requests, 1)
	r := fakeTransport.requests[0]
	assert.Equal(t, "http://agent-address:8126/v0.1/pipeline_stats", r.URL.String())
}
