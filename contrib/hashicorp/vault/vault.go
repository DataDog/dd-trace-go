// The vault package contains functions to construct an *http.Client that will
// integrate with the github.com/hashicorp/vault/api and collect traces to send
// to DataDog.
//
// The easiest way to use this package is to create an *http.Client with
// NewHTTPClient, and put it in the vault api config that's passed to the
//
// If you are already using your own *http.Client with vault, you can use the
// WrapHTTPTransport function to wrap the client with the tracer code. Your
// *http.Client will continue to work as before, but will also capture traces.
package vault

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/hashicorp/vault/api"
	"github.com/hashicorp/vault/sdk/helper/consts"
)

// NewHTTPClient returns an *http.Client for use in the config for the vault api
// Client. Options can be optionally passed in to configure various tracer features such as Analytics
func NewHTTPClient(opts ...httptrace.RoundTripperOption) *http.Client {
	dc := api.DefaultConfig()
	c := dc.HttpClient
	WrapHTTPTransport(c, opts...)
	return c
}

type vaultErrors struct {
	Errors []string `json:"errors"`
}

// WrapHTTPTransport takes an existing *http.Client and wraps the transport with
// the tracing code. This will leave the existing Transport in place underneath,
// only adding tracing code around it.
func WrapHTTPTransport(client *http.Client, opts ...httptrace.RoundTripperOption) *http.Client {
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	wrapperOpts := []httptrace.RoundTripperOption{
		httptrace.WithBefore(func(req *http.Request, span ddtrace.Span) {
			span.SetTag(ext.ServiceName, "vault")
			span.SetTag(ext.HTTPURL, req.URL.Path)
			span.SetTag(ext.HTTPMethod, req.Method)
			span.SetTag(ext.ResourceName, req.Method+" "+req.URL.Path)
			span.SetTag(ext.SpanType, ext.SpanTypeHTTP)
			if ns := req.Header.Get(consts.NamespaceHeaderName); ns != "" {
				span.SetTag("vault.namespace", ns)
			}
		}),
		httptrace.WithAfter(func(resp *http.Response, span ddtrace.Span) {
			if resp == nil {
				return
			}
			span.SetTag(ext.HTTPCode, resp.StatusCode)
			if resp.StatusCode >= 400 {
				// See: https://www.vaultproject.io/api/overview.html#error-response
				errs := []string{}
				defer resp.Body.Close()
				bodyBytes, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					errs = append(errs, err.Error())
				} else {
					resp.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))
					vaultErrors := vaultErrors{}
					err = json.Unmarshal(bodyBytes, &vaultErrors)
					if err != nil {
						errs = append(errs, err.Error())
					} else {
						errs = append(errs, vaultErrors.Errors...)
					}
				}
				span.SetTag(ext.Error, fmt.Errorf("%d: %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
				span.SetTag(ext.ErrorDetails, errs)
			}
		}),
	}
	wrapperOpts = append(wrapperOpts, opts...)
	wrapped := httptrace.WrapRoundTripper(client.Transport, wrapperOpts...)
	client.Transport = wrapped
	return client
}
