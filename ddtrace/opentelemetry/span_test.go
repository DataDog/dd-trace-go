// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"
)

func TestSpanMethods(t *testing.T) {
	testData := struct {
		env, srv, oldOp, newOp string
		tags                   [][]string // key - tags[0][0], value - tags[0][1]
	}{
		env:   "test_env",
		srv:   "test_srv",
		oldOp: "old_op",
		newOp: "new_op",
		tags:  [][]string{{"tag_1", "tag_1_val"}, {"opt_tag_1", "opt_tag_1_val"}}}
	done := make(chan struct{})
	var payload string
	s, c := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.4/traces":
			buf, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fail()
			}
			payload = fmt.Sprintf("%s", buf)
			done <- struct{}{}
		}
		w.WriteHeader(200)
	}))
	defer s.Close()

	otel.SetTracerProvider(NewTracerProvider(
		tracer.WithEnv(testData.env),
		tracer.WithHTTPClient(c),
		tracer.WithService(testData.srv)))
	tr := otel.Tracer("")
	assert := assert.New(t)

	_, sp := tr.Start(context.Background(), testData.oldOp)
	for _, tag := range testData.tags {
		sp.SetAttributes(attribute.String(tag[0], tag[1]))
	}
	sp.SetName(testData.newOp)
	// Should return true as long as span is not finished
	assert.True(sp.IsRecording())
	sp.SetStatus(codes.Error, "error_description")
	sp.End()
	// Should return false once the span is finished / ended
	assert.False(sp.IsRecording())
	tracer.Flush()

	select {
	case <-time.After(time.Second):
		t.Fatal("test server didn't compile within 1 second")
	case <-done:
		break
	}
	assert.Contains(payload, testData.env)
	assert.Contains(payload, testData.newOp)
	assert.Contains(payload, testData.srv)
	for _, tag := range testData.tags {
		assert.Contains(payload, tag[0])
		assert.Contains(payload, tag[1])
	}
}
