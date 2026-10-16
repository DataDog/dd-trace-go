// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTransport struct {
	requests []*http.Request
}

func (t *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, r)
	return &http.Response{StatusCode: 200}, nil
}

type errorTransport struct {
	statusCode int
	body       string
}

func (t *errorTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.statusCode,
		Body:       io.NopCloser(strings.NewReader(t.body)),
	}, nil
}

func TestHTTPTransportWithTransactions(t *testing.T) {
	p := StatsPayload{
		Env:         "env-1",
		ProductMask: productAPM | productDSM,
		Stats: []StatsBucket{{
			Start:                    2,
			Duration:                 10,
			Transactions:             []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 3, 't', 'x', '1'},
			TransactionCheckpointIds: []byte{1, 3, 'i', 'n', 'g'},
		}},
	}
	ft := fakeTransport{}
	transport := newHTTPTransport(&url.URL{Scheme: "http", Host: "agent-address:8126"}, &http.Client{Transport: &ft})
	require.Nil(t, transport.sendPipelineStats(&p))
	assert.Len(t, ft.requests, 1)
}

func TestHTTPTransportError(t *testing.T) {
	et := &errorTransport{statusCode: 400, body: "bad request body"}
	transport := newHTTPTransport(&url.URL{Scheme: "http", Host: "agent-address:8126"}, &http.Client{Transport: et})
	err := transport.sendPipelineStats(&StatsPayload{Env: "env-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad request body")
}

func TestHTTPTransportErrorNoBody(t *testing.T) {
	et := &errorTransport{statusCode: 500, body: ""}
	transport := newHTTPTransport(&url.URL{Scheme: "http", Host: "agent-address:8126"}, &http.Client{Transport: et})
	err := transport.sendPipelineStats(&StatsPayload{Env: "env-1"})
	require.Error(t, err)
}

func TestHTTPTransport(t *testing.T) {
	p := StatsPayload{Env: "env-1", Stats: []StatsBucket{{
		Start:    2,
		Duration: 10,
		Stats: []StatsPoint{{
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

func TestHTTPTransportReusesConnection(t *testing.T) {
	var newConns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"acknowledged":true,"error":""}`))
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			newConns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	agentURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	transport := newHTTPTransport(agentURL, client)

	const flushes = 5
	for range flushes {
		require.NoError(t, transport.sendPipelineStats(&StatsPayload{Env: "env-1"}))
	}
	assert.Equal(t, int32(1), newConns.Load(), "each pipeline_stats flush should reuse the keep-alive connection to the agent")
}
