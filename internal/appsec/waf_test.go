// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	pAppsec "github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/ossec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	httptrace "github.com/DataDog/dd-trace-go/v2/instrumentation/httptracemock"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/apisec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/body"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/timer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCustomRules(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/custom_rules.json")
	testutils.StartAppSec(t)

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, tc := range []struct {
		name      string
		method    string
		ruleMatch string
	}{
		{
			name:      "method",
			method:    "POST",
			ruleMatch: "custom-001",
		},
		{
			name:   "no-method",
			method: "GET",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			telemetryClient := new(telemetrytest.RecordClient)
			prevClient := telemetry.SwapClient(telemetryClient)
			defer telemetry.SwapClient(prevClient)

			req, err := http.NewRequest(tc.method, srv.URL, nil)
			require.NoError(t, err)

			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			event := spans[0].Tag("_dd.appsec.json")

			if tc.ruleMatch != "" {
				require.NotNil(t, event)
				require.Contains(t, event, tc.ruleMatch)
			}

			assert.Equal(t, 1.0, telemetryClient.Count(telemetry.NamespaceAppSec, "waf.requests", []string{
				"request_blocked:false",
				"rule_triggered:" + strconv.FormatBool(tc.ruleMatch != ""),
				"waf_timeout:false",
				"rate_limited:false",
				"waf_error:false",
				"waf_version:" + libddwaf.Version(),
				"event_rules_version:1.4.2",
				"input_truncated:false",
			}).Get())
		})
	}
}

func TestUserRules(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/user_rules.json")
	testutils.StartAppSec(t)

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	mux.HandleFunc("/response-header", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("match-response-header", "match-response-header")
		w.WriteHeader(204)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, tc := range []struct {
		name string
		url  string
		rule string
	}{
		{
			name: "custom-001",
			url:  "/hello",
			rule: "custom-001",
		},
		{
			name: "custom-action",
			url:  "/hello?match=match-request-query",
			rule: "query-002",
		},
		{
			name: "response-headers",
			url:  "/response-header",
			rule: "headers-003",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest("GET", srv.URL+tc.url, nil)
			require.NoError(t, err)

			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			event := spans[0].Tag("_dd.appsec.json")
			require.Contains(t, event, tc.rule)

		})
	}
}

// TestWAF is a simple validation test of the WAF protecting a net/http server. It only mockups the agent and tests that
// the WAF is properly detecting an LFI attempt and that the corresponding security event is being sent to the agent.
// Additionally, verifies that rule matching through SDK body instrumentation works as expected
func TestWAF(t *testing.T) {
	testutils.StartAppSec(t)

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	mux.HandleFunc("/body", func(w http.ResponseWriter, r *http.Request) {
		pAppsec.MonitorParsedHTTPBody(r.Context(), "$globals")
		w.Write([]byte("Hello Body!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("lfi", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send an LFI attack
		req, err := http.NewRequest("POST", srv.URL+"/../../../secret.txt", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		// Check that the handler was properly called
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 2)

		// Two requests were performed by the client request (due to the 301 redirection) and the two should have the LFI
		// attack attempt event (appsec rule id crs-930-110).
		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.Contains(t, event, "crs-930-110")

		event = finished[1].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.Contains(t, event, "crs-930-110")
	})

	// Test a PHP injection attack via request parsed body
	t.Run("SDK-body", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srv.URL+"/body", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		// Check that the handler was properly called
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello Body!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.True(t, strings.Contains(event.(string), "crs-933-130"))
	})

	t.Run("obfuscation", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a malicious request with sensitive data that should be both
		// obfuscated by the obfuscator key and value regexps.
		form := url.Values{}

		// Form value detected by a XSS attack that should be obfuscated by the
		// obfuscator value regex.
		const sensitivePayloadValue = `BEARER lwqjedqwdoqwidmoqwndun32i`
		form.Add("payload", `
{
   "activeTab":"39612314-1890-45f7-8075-c793325c1d70",
   "allOpenTabs":["132ef2e5-afaa-4e20-bc64-db9b13230a","39612314-1890-45f7-8075-c793325c1d70"],
   "lastPage":{
       "accessToken":"`+sensitivePayloadValue+`",
       "account":{
           "name":"F123123 .htaccess",
           "contactCustomFields":{
               "ffa77959-1ff3-464b-a3af-e5410e436f1f":{
                   "questionServiceEntityType":"CustomField",
                   "question":{
                       "code":"Manager Name",
                       "questionTypeInfo":{
                           "questionType":"OpenEndedText",
                           "answerFormatType":"General"
                           ,"scores":[]
                       },
                   "additionalInfo":{
                       "codeSnippetValue":"<!-- Google Tag Manager (noscript) -->\r\n<embed src=\"https://www.googletagmanager.com/ns.html?id=GTM-PCVXQNM\"\r\nheight=\"0\" width=\"0\" style=\"display:none"
                   }
               }
           }
       }
   }
}
`)
		// Form value detected by a SQLi rule that should be obfuscated by the
		// obfuscator value regex.
		sensitiveParamKeyValue := "I-m-sensitive-please-dont-expose-me"
		form.Add("password", sensitiveParamKeyValue+"1234%20union%20select%20*%20from%20credit_cards"+sensitiveParamKeyValue)

		req, err := http.NewRequest("POST", srv.URL, nil)
		req.URL.RawQuery = form.Encode()

		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		// Check that the handler was properly called
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check that the tags don't hold any sensitive information
		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.Contains(t, event, "crs-942-270") // SQLi
		require.Contains(t, event, "crs-941-230") // XSS
		require.NotContains(t, event, sensitiveParamKeyValue)
		require.NotContains(t, event, sensitivePayloadValue)
	})
}

// Test that request blocking works by using custom rules/rules data
func TestBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/blocking.json")
	testutils.StartAppSec(t)

	if !appsec.Enabled() {
		t.Skip("AppSec needs to be enabled for this test")
	}

	const (
		ipBlockingRule   = "blk-001-001"
		userBlockingRule = "blk-001-002"
		bodyBlockingRule = "crs-933-130-block"
	)

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/ip", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if err := pAppsec.SetUser(r.Context(), r.Header.Get("test-usr")); err != nil {
			return
		}
		w.Write([]byte("Hello World!\n"))
	})
	mux.HandleFunc("/body", func(w http.ResponseWriter, r *http.Request) {
		buf := new(strings.Builder)
		io.Copy(buf, r.Body)
		if err := pAppsec.MonitorParsedHTTPBody(r.Context(), buf.String()); err != nil {
			return
		}
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var payload struct {
		Triggers []struct {
			SecurityResponseID string `json:"security_response_id"`
		} `json:"triggers"`
	}

	for _, tc := range []struct {
		name      string
		headers   map[string]string
		endpoint  string
		status    int
		ruleMatch string
		reqBody   string
	}{
		{
			name:     "ip/no-block/no-ip",
			endpoint: "/ip",
			status:   200,
		},
		{
			name:     "ip/no-block/good-ip",
			endpoint: "/ip",
			headers:  map[string]string{"x-forwarded-for": "1.2.3.5"},
			status:   200,
		},
		{
			name:      "ip/block",
			headers:   map[string]string{"x-forwarded-for": "1.2.3.4"},
			endpoint:  "/ip",
			status:    403,
			ruleMatch: ipBlockingRule,
		},
		{
			name:     "user/no-block/no-user",
			endpoint: "/user",
			status:   200,
		},
		{
			name:     "user/no-block/legit-user",
			headers:  map[string]string{"test-usr": "legit-user"},
			endpoint: "/user",
			status:   200,
		},
		{
			name:      "user/block",
			headers:   map[string]string{"test-usr": "blocked-user-1"},
			endpoint:  "/user",
			status:    403,
			ruleMatch: userBlockingRule,
		},
		// This test checks that IP blocking happens BEFORE user blocking, since user blocking needs the request handler
		// to be invoked while IP blocking doesn't
		{
			name:      "user/ip-block",
			headers:   map[string]string{"test-usr": "blocked-user-1", "x-forwarded-for": "1.2.3.4"},
			endpoint:  "/user",
			status:    403,
			ruleMatch: ipBlockingRule,
		},
		{
			name:     "body/no-block",
			endpoint: "/body",
			status:   200,
			reqBody:  "Happy body existing",
		},
		{
			name:      "body/block",
			endpoint:  "/body",
			status:    403,
			reqBody:   "$globals",
			ruleMatch: bodyBlockingRule,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			telemetryClient := new(telemetrytest.RecordClient)
			prevClient := telemetry.SwapClient(telemetryClient)
			defer telemetry.SwapClient(prevClient)
			req, err := http.NewRequest("POST", srv.URL+tc.endpoint, strings.NewReader(tc.reqBody))
			require.NoError(t, err)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			require.Equal(t, tc.status, res.StatusCode)
			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			if tc.status == 200 {
				require.Equal(t, "Hello World!\n", string(b))
			} else {
				require.NotEqual(t, "Hello World!\n", string(b))
			}
			if tc.ruleMatch != "" {
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				require.Contains(t, spans[0].Tag("_dd.appsec.json"), tc.ruleMatch)
				if tc.status != 200 {
					securityEvent, ok := spans[0].Tag("_dd.appsec.json").(string)
					require.True(t, ok)
					require.Contains(t, spans[0].Tag("_dd.appsec.json"), "security_response_id")
					require.NoError(t, json.Unmarshal([]byte(securityEvent), &payload))
					require.Contains(t, string(b), payload.Triggers[0].SecurityResponseID)
				}
			}

			assert.Equal(t, 1.0, telemetryClient.Count(telemetry.NamespaceAppSec, "waf.requests", []string{
				"request_blocked:" + strconv.FormatBool(tc.status != 200),
				"rule_triggered:" + strconv.FormatBool(tc.ruleMatch != ""),
				"waf_timeout:false",
				"rate_limited:false",
				"waf_error:false",
				"waf_version:" + libddwaf.Version(),
				"event_rules_version:1.4.2",
				"input_truncated:false",
			}).Get())
		})
	}
}

// Test that API Security schemas get collected when API security is enabled
func TestAPISecurity(t *testing.T) {
	// Start and trace an HTTP server
	t.Setenv(config.EnvEnabled, "true")
	if wafOK, err := libddwaf.Usable(); !wafOK {
		t.Skipf("WAF must be usable for this test to run correctly: %v", err)
	}
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/apisec/{id}", func(w http.ResponseWriter, r *http.Request) {
		pAppsec.MonitorParsedHTTPBody(r.Context(), "plain body")
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/apisec/1337?vin=AAAAAAAAAAAAAAAAA", nil)
	require.NoError(t, err)

	t.Run("enabled", func(t *testing.T) {
		var sampler mockSampler
		samplingKey := apisec.SamplingKey{
			Method:     "POST",
			Route:      "/apisec/{id}",
			StatusCode: 200,
		}
		sampler.On("DecisionFor", samplingKey).Return(true).Once()

		t.Setenv(config.EnvAPISecEnabled, "true")
		testutils.StartAppSec(t, config.WithAPISecOptions(config.WithAPISecSampler(&sampler)))
		require.True(t, appsec.Enabled())

		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		// Verify we did make a sampling decision as expected...
		sampler.AssertCalled(t, "DecisionFor", samplingKey)

		// Make sure the addresses that are present are getting extracted as schemas
		assert.NotNil(t, spans[0].Tag("_dd.appsec.s.req.headers"))
		assert.NotNil(t, spans[0].Tag("_dd.appsec.s.req.query"))
		assert.NotNil(t, spans[0].Tag("_dd.appsec.s.req.body"))

		t.Run("sampler-drops-second-request", func(t *testing.T) {
			sampler.On("DecisionFor", samplingKey).Return(false).Once()

			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2) // Includes the span from the first request...

			// Verify we did make a sampling decision as expected...
			sampler.AssertCalled(t, "DecisionFor", samplingKey)

			// Make sure that the schema has NOT been extracted
			assert.Nil(t, spans[1].Tag("_dd.appsec.s.req.headers"))
			assert.Nil(t, spans[1].Tag("_dd.appsec.s.req.query"))
			assert.Nil(t, spans[1].Tag("_dd.appsec.s.req.body"))
		})
	})

	t.Run("disabled", func(t *testing.T) {
		var sampler mockSampler

		t.Setenv(config.EnvAPISecEnabled, "false")
		testutils.StartAppSec(t, config.WithAPISecOptions(config.WithAPISecSampler(&sampler)))
		require.True(t, appsec.Enabled())

		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		// Make sure the sampler was never called
		sampler.AssertNotCalled(t, "DecisionFor")

		// Make sure the addresses that are present are not getting extracted as schemas
		require.Nil(t, spans[0].Tag("_dd.appsec.s.req.headers"))
		require.Nil(t, spans[0].Tag("_dd.appsec.s.req.query"))
		require.Nil(t, spans[0].Tag("_dd.appsec.s.req.body"))
	})
}

func TestAPISecurityProxy(t *testing.T) {
	if wafOK, err := libddwaf.Usable(); !wafOK {
		t.Skipf("WAF must be usable for this test to run correctly: %v", err)
	}

	mux := httptrace.NewServeMux()
	mux.HandleFunc("/apisec/{id}", func(w http.ResponseWriter, r *http.Request) {
		pAppsec.MonitorParsedHTTPBody(r.Context(), "plain body")
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/apisec/1337?vin=AAAAAAAAAAAAAAAAA", nil)
	require.NoError(t, err)

	t.Run("rate-limits", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "true")
		t.Setenv(config.EnvAPISecEnabled, "true")
		// Set the rate to 1 schema per minute
		t.Setenv(config.EnvAPISecProxySampleRate, "1")
		testutils.StartAppSec(t, config.WithAPISecOptions(config.WithProxy()))
		require.True(t, appsec.Enabled())

		mt := mocktracer.Start()
		defer mt.Stop()

		// First request should be sampled
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.NotNil(t, spans[0].Tag("_dd.appsec.s.req.query"))

		// Second request should be dropped
		res, err = srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		spans = mt.FinishedSpans()
		require.Len(t, spans, 2)
		assert.Nil(t, spans[1].Tag("_dd.appsec.s.req.query"))
	})

	t.Run("disabled-with-rate-0", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "true")
		t.Setenv(config.EnvAPISecEnabled, "true")
		t.Setenv(config.EnvAPISecProxySampleRate, "0")
		testutils.StartAppSec(t, config.WithAPISecOptions(config.WithProxy()))
		require.True(t, appsec.Enabled())

		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Nil(t, spans[0].Tag("_dd.appsec.s.req.query"))
	})
}

func TestRASPLFI(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/rasp.json")
	testutils.StartAppSec(t)

	if !appsec.RASPEnabled() {
		t.Skip("RASP needs to be enabled for this test")
	}

	// Simulate what orchestrion does
	WrappedOpen := func(ctx context.Context, path string, flags int) (file *os.File, err error) {
		parent, _ := dyngo.FromContext(ctx)
		op := &ossec.OpenOperation{
			Operation: dyngo.NewOperation(parent),
		}

		dyngo.StartOperation(op, ossec.OpenOperationArgs{
			Path:  path,
			Flags: flags,
			Perms: fs.FileMode(0),
		})

		defer dyngo.FinishOperation(op, ossec.OpenOperationRes[*os.File]{
			File: &file,
			Err:  &err,
		})

		return
	}

	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Subsequent spans inherit their parent from context.
		path := r.URL.Query().Get("path")
		block := r.URL.Query().Get("block")
		if block == "true" {
			_, err := WrappedOpen(r.Context(), path, os.O_RDONLY)
			require.ErrorIs(t, err, &events.BlockingSecurityEvent{})
			return
		}

		_, err := WrappedOpen(r.Context(), "/tmp/test", os.O_RDWR)
		require.NoError(t, err)
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, tc := range []struct {
		name  string
		path  string
		block bool
	}{
		{
			name:  "no-error",
			path:  "",
			block: false,
		},
		{
			name:  "passwd",
			path:  "/etc/passwd",
			block: true,
		},
		{
			name:  "shadow",
			path:  "/etc/shadow",
			block: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			telemetryClient := new(telemetrytest.RecordClient)
			prevClient := telemetry.SwapClient(telemetryClient)
			defer telemetry.SwapClient(prevClient)

			req, err := http.NewRequest("GET", srv.URL+"?path="+tc.path+"&block="+strconv.FormatBool(tc.block), nil)
			require.NoError(t, err)
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			if tc.block {
				require.Equal(t, 403, res.StatusCode)
				require.Contains(t, spans[0].Tag("_dd.appsec.json"), "rasp-930-100")
				require.Contains(t, spans[0].Tags(), "_dd.stack")
			} else {
				require.Equal(t, 204, res.StatusCode)
			}

			assert.Equal(t, 1.0, telemetryClient.Count(telemetry.NamespaceAppSec, "rasp.rule.eval", []string{
				"rule_type:lfi",
				"waf_version:" + libddwaf.Version(),
				"event_rules_version:1.4.2",
			}).Get())

			if !tc.block {
				return
			}

			assert.Equal(t, 1.0, telemetryClient.Count(telemetry.NamespaceAppSec, "rasp.rule.match", []string{
				"block:success",
				"rule_type:lfi",
				"waf_version:" + libddwaf.Version(),
				"event_rules_version:1.4.2",
			}).Get())
		})
	}
}

func TestSuspiciousAttackerBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/sab.json")
	testutils.StartAppSec(t)

	if !appsec.Enabled() {
		t.Skip("AppSec needs to be enabled for this test")
	}

	const bodyBlockingRule = "crs-933-130-block"

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := pAppsec.SetUser(r.Context(), r.Header.Get("test-usr")); err != nil {
			return
		}
		buf := new(strings.Builder)
		io.Copy(buf, r.Body)
		if err := pAppsec.MonitorParsedHTTPBody(r.Context(), buf.String()); err != nil {
			return
		}
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, tc := range []struct {
		name      string
		headers   map[string]string
		status    int
		ruleMatch string
		attack    string
	}{
		{
			name:   "ip/not-suspicious/no-attack",
			status: 200,
		},
		{
			name:    "ip/suspicious/no-attack",
			headers: map[string]string{"x-forwarded-for": "1.2.3.4"},
			status:  200,
		},
		{
			name:      "ip/not-suspicious/attack",
			status:    200,
			attack:    "$globals",
			ruleMatch: bodyBlockingRule,
		},
		{
			name:      "ip/suspicious/attack",
			headers:   map[string]string{"x-forwarded-for": "1.2.3.4"},
			status:    402,
			attack:    "$globals",
			ruleMatch: bodyBlockingRule,
		},
		{
			name:   "user/not-suspicious/no-attack",
			status: 200,
		},
		{
			name:    "user/suspicious/no-attack",
			headers: map[string]string{"test-usr": "blocked-user-1"},
			status:  200,
		},
		{
			name:      "user/not-suspicious/attack",
			status:    200,
			attack:    "$globals",
			ruleMatch: bodyBlockingRule,
		},
		{
			name:      "user/suspicious/attack",
			headers:   map[string]string{"test-usr": "blocked-user-1"},
			status:    401,
			attack:    "$globals",
			ruleMatch: bodyBlockingRule,
		},
		{
			name:    "ip+user/suspicious/no-attack",
			headers: map[string]string{"x-forwarded-for": "1.2.3.4", "test-usr": "blocked-user-1"},
			status:  200,
		},
		{
			name:      "ip+user/suspicious/attack",
			headers:   map[string]string{"x-forwarded-for": "1.2.3.4", "test-usr": "blocked-user-1"},
			status:    402,
			attack:    "$globals",
			ruleMatch: bodyBlockingRule,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			req, err := http.NewRequest("POST", srv.URL, strings.NewReader(tc.attack))
			require.NoError(t, err)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			if tc.ruleMatch != "" {
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				require.Contains(t, spans[0].Tag("_dd.appsec.json"), tc.ruleMatch)
			}
			require.Equal(t, tc.status, res.StatusCode)
			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			if tc.status == 200 {
				require.Equal(t, "Hello World!\n", string(b))
			} else {
				require.NotEqual(t, "Hello World!\n", string(b))
			}
		})
	}
}

func TestWafEventsInMetaStruct(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/user_rules.json")
	appsec.Start(config.WithMetaStructAvailable(true))
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	mux.HandleFunc("/response-header", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("match-response-header", "match-response-header")
		w.WriteHeader(204)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, tc := range []struct {
		name string
		url  string
		rule string
	}{
		{
			name: "custom-001",
			url:  "/hello",
			rule: "custom-001",
		},
		{
			name: "custom-action",
			url:  "/hello?match=match-request-query",
			rule: "query-002",
		},
		{
			name: "response-headers",
			url:  "/response-header",
			rule: "headers-003",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest("GET", srv.URL+tc.url, nil)
			require.NoError(t, err)

			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)

			events, ok := spans[0].Tag("appsec").(map[string][]any)
			require.True(t, ok)

			triggers := events["triggers"]
			ids := make([]string, 0, len(triggers))
			for _, trigger := range triggers {
				ids = append(ids, trigger.(map[string]any)["rule"].(map[string]any)["id"].(string))
			}

			require.Contains(t, ids, tc.rule)
		})
	}

}

// BenchmarkSampleWAFContext benchmarks the creation of a WAF context and running the WAF on a request/response pair
// This is a basic sample of what could happen in a real-world scenario.
func BenchmarkSampleWAFContext(b *testing.B) {
	builder, err := libddwaf.NewBuilder(config.DefaultObfuscatorKeyRegex, config.DefaultObfuscatorValueRegex)
	require.NoError(b, err)
	defer builder.Close()

	_, err = builder.AddDefaultRecommendedRuleset()
	require.NoError(b, err)

	handle := builder.Build()
	require.NotNil(b, handle)

	for i := 0; i < b.N; i++ {
		ctx, err := handle.NewContext(timer.WithBudget(time.Second))
		if err != nil || ctx == nil {
			b.Fatal("nil context")
		}

		// Request WAF Run
		_, err = ctx.Run(
			libddwaf.RunAddressData{
				Persistent: map[string]any{
					addresses.ClientIPAddr:            "1.1.1.1",
					addresses.ServerRequestMethodAddr: "GET",
					addresses.ServerRequestRawURIAddr: "/",
					addresses.ServerRequestHeadersNoCookiesAddr: map[string][]string{
						"host":            {"example.com"},
						"content-length":  {"0"},
						"Accept":          {"application/json"},
						"User-Agent":      {"curl/7.64.1"},
						"Accept-Encoding": {"gzip"},
						"Connection":      {"close"},
					},
					addresses.ServerRequestCookiesAddr: map[string][]string{
						"cookie": {"session=1234"},
					},
					addresses.ServerRequestQueryAddr: map[string][]string{
						"query": {"value"},
					},
					addresses.ServerRequestPathParamsAddr: map[string]string{
						"param": "value",
					},
				},
			})

		if err != nil {
			b.Fatalf("error running waf: %v", err)
		}

		// Response WAF Run
		_, err = ctx.Run(
			libddwaf.RunAddressData{
				Persistent: map[string]any{
					addresses.ServerResponseHeadersNoCookiesAddr: map[string][]string{
						"content-type":   {"application/json"},
						"content-length": {"0"},
						"Connection":     {"close"},
					},
					addresses.ServerResponseStatusAddr: 200,
				},
			})

		if err != nil {
			b.Fatalf("error running waf: %v", err)
		}

		ctx.Close()
	}
}

func TestAttackerFingerprinting(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/fp.json")
	testutils.StartAppSec(t)

	if !appsec.Enabled() {
		t.Skip("AppSec needs to be enabled for this test")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		pAppsec.TrackUserLoginSuccess(
			r.Context(),
			"toto",
			"",
			map[string]string{},
			tracer.WithUserSessionID("sessionID"))

		pAppsec.MonitorParsedHTTPBody(r.Context(), map[string]string{"key": "value"})

		w.Write([]byte("Hello World!\n"))
	})

	mux.HandleFunc("/id/auth/v1/login", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, tc := range []struct {
		name string
		url  string
	}{
		{
			name: "SDK",
			url:  "/test?x=1",
		},
		{
			name: "WAF",
			url:  "/?x=$globals",
		},
		{
			name: "CustomRule",
			url:  "/id/auth/v1/login",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest("POST", srv.URL+tc.url, nil)
			require.NoError(t, err)
			req.AddCookie(&http.Cookie{Name: "cookie", Value: "value"})
			resp, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			require.Len(t, mt.FinishedSpans(), 1)

			tags := mt.FinishedSpans()[0].Tags()

			require.Contains(t, tags, "_dd.appsec.fp.http.header")
			require.Contains(t, tags, "_dd.appsec.fp.http.endpoint")
			require.Contains(t, tags, "_dd.appsec.fp.http.network")
			require.Contains(t, tags, "_dd.appsec.fp.session")

			require.Regexp(t, `^hdr-`, tags["_dd.appsec.fp.http.header"])
			require.Regexp(t, `^http-`, tags["_dd.appsec.fp.http.endpoint"])
			require.Regexp(t, `^ssn-`, tags["_dd.appsec.fp.session"])
			require.Regexp(t, `^net-`, tags["_dd.appsec.fp.http.network"])
		})

	}
}

func TestAPI10ResponseBody(t *testing.T) {
	if ok, err := libddwaf.Usable(); !ok {
		t.Skipf("WAF must be usable for this test to run correctly: %v", err)
	}

	builder, err := libddwaf.NewBuilder("", "")
	require.NoError(t, err)

	var ruleset any

	_, thisFile, _, _ := runtime.Caller(0)

	fp, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "testdata", "api10.json"))
	require.NoError(t, err)

	err = json.Unmarshal(fp, &ruleset)
	require.NoError(t, err)

	builder.AddOrUpdateConfig("/custom", ruleset)
	defer builder.Close()

	handle := builder.Build()
	require.NotNil(t, handle)

	defer handle.Close()

	ctx, err := handle.NewContext(timer.WithUnlimitedBudget(), timer.WithComponents(addresses.Scopes[:]...))
	require.NoError(t, err)

	defer ctx.Close()

	reader := io.NopCloser(strings.NewReader(`{"payload":{"payload_out":"kqehf09123r4lnksef"},"status":"OK"}`))

	encodable, err := body.NewEncodable("application/json", &reader, 999999)
	require.NoError(t, err)

	result, err := ctx.Run(libddwaf.RunAddressData{
		Ephemeral: map[string]any{
			addresses.ServerIONetResponseBodyAddr: encodable,
		},
	})

	require.NoError(t, err)

	require.Contains(t, result.Derivatives, "_dd.appsec.trace.res_body")
}

type mockSampler struct {
	mock.Mock
}

func (m *mockSampler) DecisionFor(key apisec.SamplingKey) bool {
	ret := m.Called(key)
	return ret.Bool(0)
}

func init() {
	// This permits running the tests locally without defining the env var manually
	// We do this because the default go-libddwaf timeout value is too small and makes the tests timeout for no reason
	if _, ok := os.LookupEnv(config.EnvWAFTimeout); !ok {
		os.Setenv(config.EnvWAFTimeout, "1s")
	}
}
