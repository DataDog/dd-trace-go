// Package elastic provides functions to trace the gopkg.in/olivere/elastic.v{3,5} packages.
package elastic

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

// NewHTTPClient returns a new http.Client which traces requests under the given service name.
//
// TODO(gbbr): Remove tracer argument when we switch to OpenTracing.
func NewHTTPClient(service string, tracer *tracer.Tracer) *http.Client {
	return &http.Client{Transport: &httpTransport{&http.Transport{
		// http.DefaultTransport
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}, tracer, service}}
}

// NewHTTPClientWithTransport returns a new http.Client that traces requests using
// the given http.Transport.
//
// TODO(gbbr): Remove tracer argument when we switch to OpenTracing.
func NewHTTPClientWithTransport(transport *http.Transport, service string, tracer *tracer.Tracer) *http.Client {
	return &http.Client{Transport: &httpTransport{transport, tracer, service}}
}

// httpTransport is a traced HTTP transport that captures Elasticsearch spans.
type httpTransport struct {
	*http.Transport
	tracer  *tracer.Tracer
	service string
}

// maxContentLength is the maximum content length for which we'll read and capture
// the contents of the request body. Anything larger will still be traced but the
// body will not be captured as trace metadata.
const maxContentLength = 500 * 1024

// RoundTrip satisfies the RoundTripper interface, wraps the sub Transport and
// captures a span of the Elasticsearch request.
func (t *httpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	span := t.tracer.NewChildSpanFromContext("elasticsearch.query", req.Context())
	defer span.Finish()

	span.Service = t.service
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
	res, err := t.Transport.RoundTrip(req)
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
