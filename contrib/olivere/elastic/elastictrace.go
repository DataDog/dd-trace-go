// Package elastic provides functions to trace the gopkg.in/olivere/elastic.v{3,5} packages.
package elastic // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/olivere/elastic"

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// NewHTTPClient returns a new http.Client which traces requests under the given service name.
func NewHTTPClient(opts ...ClientOption) *http.Client {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return &http.Client{Transport: &httpTransport{config: cfg}}
}

// spanType the value that `span.type` is set to when sending
// ElasticSearch trace data to DataDog.
const spanType = "elasticsearch"

// httpTransport is a traced HTTP transport that captures Elasticsearch spans.
type httpTransport struct{ config *clientConfig }

// bodyCutoff specifies the maximum number of bytes that will be stored as a tag
// value obtained from an HTTP request or response body.
var bodyCutoff = 5 * 1024

// RoundTrip satisfies the RoundTripper interface, wraps the sub Transport and
// captures a span of the Elasticsearch request.
func (t *httpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.Path
	method := req.Method
	resource := quantize(url, method)
	span, _ := tracer.StartSpanFromContext(req.Context(), "elasticsearch.query",
		tracer.ServiceName(t.config.serviceName),
		tracer.SpanType(spanType),
		tracer.ResourceName(resource),
		tracer.Tag("elasticsearch.method", method),
		tracer.Tag("elasticsearch.url", url),
		tracer.Tag("elasticsearch.params", req.URL.Query().Encode()),
	)
	defer span.Finish()

	snip, rc, err := peek(req.Body, int(req.ContentLength), bodyCutoff)
	if err == nil {
		span.SetTag("elasticsearch.body", snip)
	}
	req.Body = rc
	// process using the standard transport
	res, err := t.config.transport.RoundTrip(req)
	if err != nil {
		// roundtrip error
		span.SetTag(ext.Error, err)
	} else if res.StatusCode < 200 || res.StatusCode > 299 {
		// HTTP error
		snip, rc, err := peek(res.Body, int(res.ContentLength), bodyCutoff)
		if err != nil {
			snip = http.StatusText(res.StatusCode)
		}
		span.SetTag(ext.Error, errors.New(snip))
		res.Body = rc
	}
	if res != nil {
		span.SetTag(ext.HTTPCode, strconv.Itoa(res.StatusCode))
	}
	return res, err
}

var (
	idRegexp         = regexp.MustCompile("/([0-9]+)([/\\?]|$)")
	idPlaceholder    = []byte("/?$2")
	indexRegexp      = regexp.MustCompile("[0-9]{2,}")
	indexPlaceholder = []byte("?")
)

// quantize quantizes an Elasticsearch to extract a meaningful resource from the request.
// We quantize based on the method+url with some cleanup applied to the URL.
// URLs with an ID will be generalized as will (potential) timestamped indices.
func quantize(url, method string) string {
	quantizedURL := idRegexp.ReplaceAll([]byte(url), idPlaceholder)
	quantizedURL = indexRegexp.ReplaceAll(quantizedURL, indexPlaceholder)
	return fmt.Sprintf("%s %s", method, quantizedURL)
}

// peek attempts to return the first n bytes, as a string, from the provided io.ReadCloser.
// It returns a new io.ReadCloser which points to the same underlying stream and can be read
// from to access the entire data including the snippet. max is used to specify the length
// of the stream contained in the reader. If unknown, it should be -1. If 0 < max < n it
// will override n.
func peek(rc io.ReadCloser, max int, n int) (string, io.ReadCloser, error) {
	if rc == nil {
		return "", rc, errors.New("empty stream")
	}
	if max > 0 && max < n {
		n = max
	}
	r := bufio.NewReaderSize(rc, n)
	rc2 := struct {
		io.Reader
		io.Closer
	}{
		Reader: r,
		Closer: rc,
	}
	snip, err := r.Peek(n)
	if err == io.EOF {
		err = nil
	}
	return string(snip), rc2, err
}
