// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	internal "github.com/DataDog/appsec-internal-go/appsec"
	waf "github.com/DataDog/go-libddwaf/v3"

	pAppsec "gopkg.in/DataDog/dd-trace-go.v1/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/ossec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/httpsec"

	_ "github.com/glebarez/go-sqlite"
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
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	mux.HandleFunc("/response-header", func(w http.ResponseWriter, r *http.Request) {
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

// Test that API Security schemas get collected when API security is enabled
func TestAPISecurity(t *testing.T) {
	// Start and trace an HTTP server
	t.Setenv(config.EnvEnabled, "true")
	if wafOK, err := waf.Health(); !wafOK {
		t.Skipf("WAF must be usable for this test to run correctly: %v", err)
	}
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/apisec", func(w http.ResponseWriter, r *http.Request) {
		pAppsec.MonitorParsedHTTPBody(r.Context(), "plain body")
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/apisec?vin=AAAAAAAAAAAAAAAAA", nil)
	require.NoError(t, err)

	t.Run("enabled", func(t *testing.T) {
		t.Setenv(internal.EnvAPISecEnabled, "true")
		t.Setenv(internal.EnvAPISecSampleRate, "1.0")
		appsec.Start()
		require.True(t, appsec.Enabled())
		defer appsec.Stop()
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		// Make sure the addresses that are present are getting extracted as schemas
		require.NotNil(t, spans[0].Tag("_dd.appsec.s.req.headers"))
		require.NotNil(t, spans[0].Tag("_dd.appsec.s.req.query"))
		require.NotNil(t, spans[0].Tag("_dd.appsec.s.req.body"))
	})

	t.Run("disabled", func(t *testing.T) {
		t.Setenv(internal.EnvAPISecEnabled, "false")
		appsec.Start()
		require.True(t, appsec.Enabled())
		defer appsec.Stop()
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		// Make sure the addresses that are present are not getting extracted as schemas
		require.Nil(t, spans[0].Tag("_dd.appsec.s.req.headers"))
		require.Nil(t, spans[0].Tag("_dd.appsec.s.req.query"))
		require.Nil(t, spans[0].Tag("_dd.appsec.s.req.body"))
	})
}

func prepareSQLDB(nbEntries int) (*sql.DB, error) {
	const tables = `
CREATE TABLE user (
   id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
   name  text NOT NULL,
   email text NOT NULL,
   password text NOT NULL
);
CREATE TABLE product (
   id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
   name  text NOT NULL,
   category  text NOT NULL,
   price  int NOT NULL
);
`
	db, err := sqltrace.Open("sqlite", ":memory:", sqltrace.WithErrorCheck(func(err error) bool {
		return err != nil
	}))
	if err != nil {
		log.Fatalln("unexpected sql.Open error:", err)
	}

	if _, err := db.Exec(tables); err != nil {
		return nil, err
	}

	for i := 0; i < nbEntries; i++ {
		_, err := db.Exec(
			"INSERT INTO user (name, email, password) VALUES (?, ?, ?)",
			fmt.Sprintf("User#%d", i),
			fmt.Sprintf("user%d@mail.com", i),
			fmt.Sprintf("secret-password#%d", i))
		if err != nil {
			return nil, err
		}

		_, err = db.Exec(
			"INSERT INTO product (name, category, price) VALUES (?, ?, ?)",
			fmt.Sprintf("Product %d", i),
			"sneaker",
			rand.Intn(500))
		if err != nil {
			return nil, err
		}
	}

	return db, nil
}

func TestRASPSQLi(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/rasp.json")
	appsec.Start()
	defer appsec.Stop()

	if !appsec.RASPEnabled() {
		t.Skip("RASP needs to be enabled for this test")
	}
	db, err := prepareSQLDB(10)
	require.NoError(t, err)

	// Setup the http server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		// Subsequent spans inherit their parent from context.
		q := r.URL.Query().Get("query")
		rows, err := db.QueryContext(r.Context(), q)
		if events.IsSecurityError(err) {
			return
		}
		if err == nil {
			rows.Close()
		}
		w.Write([]byte("Hello World!\n"))
	})
	mux.HandleFunc("/exec", func(w http.ResponseWriter, r *http.Request) {
		// Subsequent spans inherit their parent from context.
		q := r.URL.Query().Get("query")
		_, err := db.ExecContext(r.Context(), q)
		if events.IsSecurityError(err) {
			return
		}
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for name, tc := range map[string]struct {
		query string
		err   error
	}{
		"no-error": {
			query: url.QueryEscape("SELECT 1"),
		},
		"injection/SELECT": {
			query: url.QueryEscape("SELECT * FROM users WHERE user=\"\" UNION ALL SELECT NULL;version()--"),
			err:   &events.BlockingSecurityEvent{},
		},
		"injection/UPDATE": {
			query: url.QueryEscape("UPDATE users SET pwd = \"root\" WHERE id = \"\" OR 1 = 1--"),
			err:   &events.BlockingSecurityEvent{},
		},
		"injection/EXEC": {
			query: url.QueryEscape("EXEC version(); DROP TABLE users--"),
			err:   &events.BlockingSecurityEvent{},
		},
	} {
		for _, endpoint := range []string{"/query", "/exec"} {
			t.Run(name+endpoint, func(t *testing.T) {
				// Start tracer and appsec
				mt := mocktracer.Start()
				defer mt.Stop()

				req, err := http.NewRequest("POST", srv.URL+endpoint+"?query="+tc.query, nil)
				require.NoError(t, err)
				res, err := srv.Client().Do(req)
				require.NoError(t, err)
				defer res.Body.Close()

				spans := mt.FinishedSpans()

				require.Len(t, spans, 2)

				if tc.err != nil {
					require.Equal(t, 403, res.StatusCode)

					for _, sp := range spans {
						switch sp.OperationName() {
						case "http.request":
							require.Contains(t, sp.Tag("_dd.appsec.json"), "rasp-942-100")
						case "sqlite.query":
							require.NotContains(t, sp.Tags(), "error")
						}
					}
				} else {
					require.Equal(t, 200, res.StatusCode)
				}

			})
		}
	}
}

func TestRASPLFI(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/rasp.json")
	appsec.Start()
	defer appsec.Stop()

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
		})
	}
}

// BenchmarkSampleWAFContext benchmarks the creation of a WAF context and running the WAF on a request/response pair
// This is a basic sample of what could happen in a real-world scenario.
func BenchmarkSampleWAFContext(b *testing.B) {
	rules, err := internal.DefaultRuleset()
	if err != nil {
		b.Fatalf("error loading rules: %v", err)
	}

	var parsedRuleset map[string]any
	err = json.Unmarshal(rules, &parsedRuleset)
	if err != nil {
		b.Fatalf("error parsing rules: %v", err)
	}

	handle, err := waf.NewHandle(parsedRuleset, internal.DefaultObfuscatorKeyRegex, internal.DefaultObfuscatorValueRegex)
	for i := 0; i < b.N; i++ {
		ctx, err := handle.NewContext()
		if err != nil || ctx == nil {
			b.Fatal("nil context")
		}

		// Request WAF Run
		_, err = ctx.Run(
			waf.RunAddressData{
				Persistent: map[string]any{
					httpsec.HTTPClientIPAddr:        "1.1.1.1",
					httpsec.ServerRequestMethodAddr: "GET",
					httpsec.ServerRequestRawURIAddr: "/",
					httpsec.ServerRequestHeadersNoCookiesAddr: map[string][]string{
						"host":            {"example.com"},
						"content-length":  {"0"},
						"Accept":          {"application/json"},
						"User-Agent":      {"curl/7.64.1"},
						"Accept-Encoding": {"gzip"},
						"Connection":      {"close"},
					},
					httpsec.ServerRequestCookiesAddr: map[string][]string{
						"cookie": {"session=1234"},
					},
					httpsec.ServerRequestQueryAddr: map[string][]string{
						"query": {"value"},
					},
					httpsec.ServerRequestPathParamsAddr: map[string]string{
						"param": "value",
					},
				},
			})

		if err != nil {
			b.Fatalf("error running waf: %v", err)
		}

		// Response WAF Run
		_, err = ctx.Run(
			waf.RunAddressData{
				Persistent: map[string]any{
					httpsec.ServerResponseHeadersNoCookiesAddr: map[string][]string{
						"content-type":   {"application/json"},
						"content-length": {"0"},
						"Connection":     {"close"},
					},
					httpsec.ServerResponseStatusAddr: 200,
				},
			})

		if err != nil {
			b.Fatalf("error running waf: %v", err)
		}

		ctx.Close()
	}
}

func init() {
	// This permits running the tests locally without defining the env var manually
	// We do this because the default go-libddwaf timeout value is too small and makes the tests timeout for no reason
	if _, ok := os.LookupEnv(internal.EnvWAFTimeout); !ok {
		os.Setenv(internal.EnvWAFTimeout, "1s")
	}
}
