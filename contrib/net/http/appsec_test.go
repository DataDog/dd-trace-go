// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func TestAppsec(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/rasp.json")

	client := WrapRoundTripper(&emptyRoundTripper{})

	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("DD_APPSEC_RASP_ENABLED=%v", enabled), func(t *testing.T) {
			t.Setenv("DD_APPSEC_RASP_ENABLED", strconv.FormatBool(enabled))

			mt := mocktracer.Start()
			defer mt.Stop()

			testutils.StartAppSec(t)

			w := httptest.NewRecorder()
			r, err := http.NewRequest("GET", "?value=169.254.169.254", nil)
			require.NoError(t, err)

			TraceAndServe(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				req, err := http.NewRequestWithContext(r.Context(), "GET", "http://169.254.169.254", nil)
				require.NoError(t, err)

				resp, err := client.RoundTrip(req)

				if enabled {
					require.True(t, events.IsSecurityError(err))
				} else {
					require.NoError(t, err)
				}

				if resp != nil {
					defer resp.Body.Close()
				}
			}), w, r, &ServeConfig{
				Service:  "service",
				Resource: "resource",
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2) // service entry serviceSpan & http request serviceSpan
			serviceSpan := spans[1]

			if !enabled {
				require.NotContains(t, serviceSpan.Tags(), "_dd.appsec.json")
				require.NotContains(t, serviceSpan.Tags(), "_dd.stack")
				return
			}

			require.Contains(t, serviceSpan.Tags(), "_dd.appsec.json")
			appsecJSON := serviceSpan.Tag("_dd.appsec.json")
			require.Contains(t, appsecJSON, addresses.ServerIONetURLAddr)

			require.Contains(t, serviceSpan.Tags(), "_dd.stack")
			require.NotContains(t, serviceSpan.Tags(), "error.message")

			// This is a nested event so it should contain the child span id in the service entry span
			// TODO(eliott.bouhana): uncomment this once we have the child span id in the service entry span
			// require.Contains(t, appsecJSON, `"span_id":`+strconv.FormatUint(requestSpan.SpanID(), 10))
		})
	}
}

func TestAppsecAPI10(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/api10.json")
	t.Setenv("DD_API_SECURITY_DOWNSTREAM_REQUEST_BODY_ANALYSIS_SAMPLE_RATE", "1.0")

	var b strings.Builder
	b.WriteString(`{"payload_in":"%s"`)
	for i := 0; i < 1<<12; i++ {
		b.WriteString(fmt.Sprintf(`,"%d":"b"`, i))
	}
	b.WriteString(`}`)

	for _, tc := range []struct {
		name     string
		request  func(ctx context.Context) *http.Request
		response *http.Response
		tagName  string
		tagValue string
	}{
		{
			name: "method",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "TRACE", "http://localhost:8080", nil)
				return req
			},
			tagName:  "_dd.appsec.trace.req_method",
			tagValue: "TAG_API10_REQ_METHOD",
		},
		{
			name: "headers",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				req.Header.Set("Witness", "pwq3ojtropiw3hjtowir")
				return req
			},
			tagName:  "_dd.appsec.trace.req_headers",
			tagValue: "TAG_API10_REQ_HEADERS",
		},
		{
			name: "body",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", io.NopCloser(strings.NewReader(`{"payload_in":"qw2jedrkjerbgol23ewpfirj2qw3or"}`)))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			tagName:  "_dd.appsec.trace.req_body",
			tagValue: "TAG_API10_REQ_BODY",
		},
		{
			name: "big-body",
			request: func(ctx context.Context) *http.Request {
				t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
				body := fmt.Sprintf(b.String(), "qw2jedrkjerbgol23ewpfirj2qw3or")
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", io.NopCloser(strings.NewReader(body)))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			tagName:  "_dd.appsec.trace.req_body",
			tagValue: "TAG_API10_REQ_BODY",
		},
		{
			name: "resp-status",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				return req
			},
			response: &http.Response{
				StatusCode: 201,
			},
			tagName:  "_dd.appsec.trace.res_status",
			tagValue: "TAG_API10_RES_STATUS",
		},
		{
			name: "resp-headers",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				return req
			},
			response: &http.Response{
				StatusCode: 200,
				Header: map[string][]string{
					"echo-headers": {"qwoierj12l3"},
				},
			},
			tagName:  "_dd.appsec.trace.res_headers",
			tagValue: "TAG_API10_RES_HEADERS",
		},
		{
			name: "resp-body",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				return req
			},
			response: &http.Response{
				StatusCode: 200,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(`{"payload_out":"kqehf09123r4lnksef"}`)),
			},
			tagName:  "_dd.appsec.trace.res_body",
			tagValue: "TAG_API10_RES_BODY",
		},
		{
			name: "resp-big-body",
			request: func(ctx context.Context) *http.Request {
				t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				return req
			},
			response: &http.Response{
				StatusCode: 200,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(fmt.Sprintf(b.String(), "kqehf09123r4lnksef"))),
			},
			tagName:  "_dd.appsec.trace.res_body",
			tagValue: "TAG_API10_RES_BODY",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {

			client := WrapRoundTripper(&emptyRoundTripper{customResponse: tc.response})

			mt := mocktracer.Start()
			defer mt.Stop()

			testutils.StartAppSec(t)

			w := httptest.NewRecorder()
			r, err := http.NewRequest("GET", "", nil)
			require.NoError(t, err)

			TraceAndServe(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				resp, err := client.RoundTrip(tc.request(r.Context()))
				require.NoError(t, err)
				if resp != nil && resp.Body != nil {
					defer resp.Body.Close()
				}
			}), w, r, &ServeConfig{
				Service:  "service",
				Resource: "resource",
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2) // service entry serviceSpan & http request serviceSpan
			serviceSpan := spans[1]

			require.Contains(t, serviceSpan.Tags(), tc.tagName)
			require.Equal(t, serviceSpan.Tags()[tc.tagName], tc.tagValue)

			require.Contains(t, serviceSpan.Tags(), "_dd.appsec.downstream_request")
			require.Equal(t, serviceSpan.Tags()["_dd.appsec.downstream_request"], float64(1))
		})
	}
}

func TestAppsecHTTP30X(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/api10.json")
	t.Setenv("DD_API_SECURITY_DOWNSTREAM_REQUEST_BODY_ANALYSIS_SAMPLE_RATE", "1.0")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var payload struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		require.NoError(t, r.Body.Close())

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/move":
			http.Redirect(w, r, "/redirect", http.StatusPermanentRedirect)
			err := json.NewEncoder(w).Encode(map[string]any{"redirect_style": "move"})
			require.NoError(t, err)
		case "/redirect":
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			err := json.NewEncoder(w).Encode(map[string]any{"redirect_style": "redirect"})
			require.NoError(t, err)
		case "/final":
			w.WriteHeader(http.StatusOK)
			err := json.NewEncoder(w).Encode(payload.Token)
			require.NoError(t, err)
		default:
			require.Failf(t, "unexpected request", "path: %s", r.URL.Path)
		}
	}))

	defer srv.Close()

	httpClient := WrapClient(srv.Client())

	mt := mocktracer.Start()
	defer mt.Stop()

	testutils.StartAppSec(t)

	w := httptest.NewRecorder()
	r, err := http.NewRequest("POST", srv.URL+"/move", strings.NewReader(`{"token": "kqehf09123r4lnksef"}`))
	require.NoError(t, err)
	r.Header.Set("Content-Type", "application/json")

	TraceAndServe(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		resp, err := httpClient.Do(r)
		require.NoError(t, err)
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
		}
	}), w, r, &ServeConfig{
		Service:  "service",
		Resource: "resource",
	})

	spans := mt.FinishedSpans()

	// Logically: Handler, Downstream, Redirect 1, Redirect 2
	// The last one finished is the Handler, which is the service entry span we tag onto.
	require.Len(t, spans, 4)

	serviceSpan := spans[3] // The last one closed...
	// First has been moved to /redirect...
	assert.Equal(t, "/redirect", serviceSpan.Tags()["appsec.api.redirection.move_target"], "unexpected or missing appsec.api.redirection.move_target tag")
	// Then has been redirected to /final...
	assert.Equal(t, "/final", serviceSpan.Tags()["appsec.api.redirection.redirect_target"], "unexpected or missing appsec.api.redirection.redirect_target tag")
	// And finally, has received a non-redirect response
	assert.Equal(t, float64(1), serviceSpan.Tags()["appsec.api.redirection.nothing"], "unexpected or missing appsec.api.redirection.nothing tag")
	// Which makes a total of 3 downstream requests for this redirect chain
	assert.Equal(t, float64(3), serviceSpan.Tags()["_dd.appsec.downstream_request"], "unexpected or missing _dd.appsec.downstream_request tag")

	// We have analyzed the final response body alright
	assert.Equal(t, "TAG_API10_RES_BODY", serviceSpan.Tags()["_dd.appsec.trace.res_body"], "unexpected or missing _dd.appsec.trace.res_body tag")
	// But we have not analyzed any of the redirect response bodies
	assert.NotContains(t, serviceSpan.Tags(), "_dd.appsec.trace.3xx_res_body")
}
