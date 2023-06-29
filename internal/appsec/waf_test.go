// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	pAppsec "gopkg.in/DataDog/dd-trace-go.v1/appsec"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/stretchr/testify/require"
)

func TestCustomRules(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/custom_rules.json")
	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
		})
	}
}

func TestUserRules(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/user_rules.json")
	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("custom-001", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("GET", srv.URL, nil)
		require.NoError(t, err)

		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		event := spans[0].Tag("_dd.appsec.json")
		require.Contains(t, event, "custom-001")
	})

}

// TestWAF is a simple validation test of the WAF protecting a net/http server. It only mockups the agent and tests that
// the WAF is properly detecting an LFI attempt and that the corresponding security event is being sent to the agent.
// Additionally, verifies that rule matching through SDK body instrumentation works as expected
func TestWAF(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
	appsec.Start()
	defer appsec.Stop()
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
	mux.HandleFunc("/ip", func(w http.ResponseWriter, r *http.Request) {
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
			}

		})
	}
}
