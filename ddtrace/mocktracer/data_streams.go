// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer

import (
	"compress/gzip"
	"net/http"

	"github.com/tinylib/msgp/msgp"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/datastreams"
)

type mockDSMTransport struct {
	backlogs []datastreams.Backlog
}

// RoundTrip does nothing and returns a dummy response.
func (t *mockDSMTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// You can customize the dummy response if needed.
	gzipReader, err := gzip.NewReader(req.Body)
	if err != nil {
		return nil, err
	}
	var p datastreams.StatsPayload
	err = msgp.Decode(gzipReader, &p)
	if err != nil {
		return nil, err
	}
	for _, bucket := range p.Stats {
		t.backlogs = append(t.backlogs, bucket.Backlogs...)
	}
	return &http.Response{
		StatusCode:    200,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Request:       req,
		ContentLength: -1,
		Body:          http.NoBody,
	}, nil
}
