package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestSpanOptions(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("")) }))
	defer s.Close()

	tagKey1, tagKey2 := "foo", "bar"
	tagValue1, tagValue2 := "bar", "baz"
	mt := mocktracer.Start()
	defer mt.Stop()
	rt := WrapRoundTripper(http.DefaultTransport, RTWithSpanOptions(
		func(cfg *ddtrace.StartSpanConfig) {
			cfg.Tags[tagKey1] = tagValue1
		},
		tracer.Tag(tagKey2, tagValue2),
	))
	client := &http.Client{Transport: rt}

	resp, err := client.Get(s.URL)
	assert.Nil(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	assert.Equal(t, tagValue1, spans[0].Tag(tagKey1))
	assert.Equal(t, tagValue2, spans[0].Tag(tagKey2))
}
