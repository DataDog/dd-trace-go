// The vault package contains functions to construct an *http.Client that will
// integrate with the github.com/hashicorp/vault/api and collect traces to send
// to DataDog.
//
// The easiest way to use this package is to create an *http.Client with
// NewHttpClient, and put it in the vault api config that's passed to the
// NewClient function.
//    import "github.com/hashicorp/vault/api"
//    import trace "gopkg.in/DataDog/dd-trace-go.v1/contrib/hashicorp/vault"
//
//    func ... {
//        api.NewClient(&api.Config{HttpClient: trace.NewHttpClient()})
//    }
//
// If you are already using your own *http.Client with vault, you can use the
// WrapHttpTransport function to wrap the client with the tracer code. Your
// *http.Client will continue to work as before, but will also capture traces.
//
//    func ... {
//        var myHttpClient *http.Client
//        myHttpClient = ...
//        api.NewClient(&api.Config{HttpClient: trace.WrapHttpTransport(myHttpClient)})
//    }
//
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

	vault "github.com/hashicorp/vault/api"
	"github.com/hashicorp/vault/sdk/helper/consts"
)

// NewHttpClient returns an *http.Client for use in the config for the vault api
// Client.
func NewHttpClient() *http.Client {
	dc := vault.DefaultConfig()
	c := dc.HttpClient
	WrapHttpTransport(c)
	return c
}

type vaultErrors struct {
	Errors []string `json:"errors"`
}

// WrapHttpTransport takes an existing *http.Client and wraps the transport with
// the tracing code.
func WrapHttpTransport(client *http.Client) *http.Client {
	wrapped := httptrace.WrapRoundTripper(client.Transport,
		httptrace.WithBefore(func(req *http.Request, span ddtrace.Span) {
			span.SetTag(ext.ServiceName, "vault")
			span.SetTag(ext.HTTPURL, req.URL.Path)
			span.SetTag(ext.HTTPMethod, req.Method)
			span.SetTag(ext.ResourceName, req.Method+" "+req.URL.Path)
			span.SetTag(ext.SpanType, ext.SpanTypeVault)
			namespace := req.Header.Get(consts.NamespaceHeaderName)
			if namespace != "" {
				span.SetTag("vault.namespace", namespace)
			}
		}),
		httptrace.WithAfter(func(resp *http.Response, span ddtrace.Span) {
			if resp != nil {
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
			}
		}),
	)
	client.Transport = wrapped
	return client
}
