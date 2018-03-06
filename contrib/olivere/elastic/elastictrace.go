// Package elastic provides functions to trace the gopkg.in/olivere/elastic.v{3,5} packages.
package elastic

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"

	"github.com/DataDog/dd-trace-go/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/ddtrace/tracer"
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

// httpTransport is a traced HTTP transport that captures Elasticsearch spans.
type httpTransport struct{ config *clientConfig }

// maxContentLength is the maximum content length for which we'll read and capture
// the contents of the request body. Anything larger will still be traced but the
// body will not be captured as trace metadata.
const maxContentLength = 500 * 1024

// RoundTrip satisfies the RoundTripper interface, wraps the sub Transport and
// captures a span of the Elasticsearch request.
func (t *httpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.Path
	method := req.Method
	resource := quantize(url, method)
	span, _ := tracer.StartSpanFromContext(req.Context(), "elasticsearch.query",
		tracer.ServiceName(t.config.serviceName),
		tracer.SpanType(ext.AppTypeDB),
		tracer.ResourceName(resource),
		tracer.Tag("elasticsearch.method", method),
		tracer.Tag("elasticsearch.url", url),
		tracer.Tag("elasticsearch.params", req.URL.Query().Encode()),
	)
	defer span.Finish()

	contentLength, err := strconv.Atoi(req.Header.Get("Content-Length"))
	if req.Body != nil && err != nil && contentLength < maxContentLength {
		buf, err := ioutil.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		span.SetTag("elasticsearch.body", string(buf))
		req.Body = ioutil.NopCloser(bytes.NewBuffer(buf))
	}
	// process using the standard transport
	res, err := t.config.transport.RoundTrip(req)
	if err != nil {
		// roundtrip error
		span.SetTag(ext.Error, err)
	} else if res.StatusCode < 200 || res.StatusCode > 299 {
		// HTTP error
		buf, err := ioutil.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			span.SetTag(ext.Error, errors.New(http.StatusText(res.StatusCode)))
		} else {
			span.SetTag(ext.Error, errors.New(string(buf)))
		}
		res.Body = ioutil.NopCloser(bytes.NewBuffer(buf))
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
