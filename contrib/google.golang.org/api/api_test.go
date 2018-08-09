package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	urlshortener "google.golang.org/api/urlshortener/v1"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

func TestURLShortener(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc, err := urlshortener.New(&http.Client{
		Transport: WrapRoundTripper(http.DefaultTransport),
	})
	assert.NoError(t, err)
	svc.Url.List().Do()

	t.Fatal(mt.FinishedSpans())
}
