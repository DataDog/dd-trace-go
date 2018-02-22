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

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
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
	span := t.config.tracer.NewChildSpanFromContext("elasticsearch.query", req.Context())
	defer span.Finish()

	span.Service = t.config.serviceName
	span.Type = ext.AppTypeDB
	span.SetMeta("elasticsearch.method", req.Method)
	span.SetMeta("elasticsearch.url", req.URL.Path)
	span.SetMeta("elasticsearch.params", req.URL.Query().Encode())

	contentLength, _ := strconv.Atoi(req.Header.Get("Content-Length"))
	if req.Body != nil && contentLength < maxContentLength {
		buf, err := ioutil.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		span.SetMeta("elasticsearch.body", string(buf))
		req.Body = ioutil.NopCloser(bytes.NewBuffer(buf))
	}
	// process using the standard transport
	res, err := t.config.transport.RoundTrip(req)
	if err != nil {
		// roundtrip error
		span.SetError(err)
	} else if res.StatusCode < 200 || res.StatusCode > 299 {
		// HTTP error
		buf, err := ioutil.ReadAll(res.Body)
		if err != nil {
			span.SetError(errors.New(http.StatusText(res.StatusCode)))
		} else {
			span.SetError(errors.New(string(buf)))
		}
		res.Body = ioutil.NopCloser(bytes.NewBuffer(buf))
	}
	if res != nil {
		span.SetMeta(ext.HTTPCode, strconv.Itoa(res.StatusCode))
	}

	quantize(span)

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
func quantize(span *tracer.Span) {
	url := span.GetMeta("elasticsearch.url")
	method := span.GetMeta("elasticsearch.method")

	quantizedURL := idRegexp.ReplaceAll([]byte(url), idPlaceholder)
	quantizedURL = indexRegexp.ReplaceAll(quantizedURL, indexPlaceholder)
	span.Resource = fmt.Sprintf("%s %s", method, quantizedURL)
}
