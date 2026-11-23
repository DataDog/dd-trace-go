// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httpsec

import (
	"context"
	_ "embed" // For go:embed
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/go-libddwaf/v5"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/apisec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	wafemitter "github.com/DataDog/dd-trace-go/v2/internal/appsec/emitter/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/listener/waf"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
)

func TestClientIP(t *testing.T) {
	for _, tc := range []struct {
		name             string
		addr             net.Addr
		md               map[string][]string // previously google.golang.org/grpc/metadata.MD
		expectedClientIP string
	}{
		{
			name:             "tcp-ipv4-address",
			addr:             &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 6789},
			expectedClientIP: "1.2.3.4",
		},
		{
			name:             "tcp-ipv4-address",
			addr:             &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 6789},
			md:               map[string][]string{"x-client-ip": {"127.0.0.1, 2.3.4.5"}},
			expectedClientIP: "2.3.4.5",
		},
		{
			name:             "tcp-ipv6-address",
			addr:             &net.TCPAddr{IP: net.ParseIP("::1"), Port: 6789},
			expectedClientIP: "::1",
		},
		{
			name:             "udp-ipv4-address",
			addr:             &net.UDPAddr{IP: net.ParseIP("1.2.3.4"), Port: 6789},
			expectedClientIP: "1.2.3.4",
		},
		{
			name:             "udp-ipv6-address",
			addr:             &net.UDPAddr{IP: net.ParseIP("::1"), Port: 6789},
			expectedClientIP: "::1",
		},
		{
			name: "unix-socket-address",
			addr: &net.UnixAddr{Name: "/var/my.sock"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, clientIP := ClientIPTags(tc.md, false, tc.addr.String())
			expectedClientIP, _ := netip.ParseAddr(tc.expectedClientIP)
			require.Equal(t, expectedClientIP.String(), clientIP.String())
		})
	}
}

func TestNormalizeHTTPHeaders(t *testing.T) {
	for _, tc := range []struct {
		headers  map[string][]string
		expected map[string]string
	}{
		{
			headers:  nil,
			expected: nil,
		},
		{
			headers: map[string][]string{
				"cookie": {"not-collected"},
			},
			expected: nil,
		},
		{
			headers: map[string][]string{
				"cookie":          {"not-collected"},
				"x-forwarded-for": {"1.2.3.4,5.6.7.8"},
			},
			expected: map[string]string{
				"x-forwarded-for": "1.2.3.4,5.6.7.8",
			},
		},
		{
			headers: map[string][]string{
				"cookie":          {"not-collected"},
				"x-forwarded-for": {"1.2.3.4,5.6.7.8", "9.10.11.12,13.14.15.16"},
			},
			expected: map[string]string{
				"x-forwarded-for": "1.2.3.4,5.6.7.8,9.10.11.12,13.14.15.16",
			},
		},
	} {
		headers := NormalizeHTTPHeaders(tc.headers)
		require.Equal(t, tc.expected, headers)
	}
}

type MockSpan struct {
	Tags map[string]any
}

func (m *MockSpan) SetTag(key string, value any) {
	if m.Tags == nil {
		m.Tags = make(map[string]any)
	}
	if key == ext.ManualKeep {
		if value == samplernames.AppSec {
			m.Tags[ext.ManualKeep] = true
		}
	} else {
		m.Tags[key] = value
	}
}

func TestTags(t *testing.T) {
	for _, eventCase := range []struct {
		name          string
		events        []any
		expectedTag   string
		expectedError bool
	}{
		{
			name:   "no-event",
			events: nil,
		},
		{
			name:        "one-event",
			events:      []any{"one"},
			expectedTag: `{"triggers":["one"]}`,
		},
		{
			name:        "two-events",
			events:      []any{"one", "two"},
			expectedTag: `{"triggers":["one","two"]}`,
		},
	} {
		for _, reqHeadersCase := range []struct {
			name         string
			headers      map[string][]string
			expectedTags map[string]any
		}{
			{
				name: "zero-headers",
			},
			{
				name: "xff-header",
				headers: map[string][]string{
					"X-Forwarded-For": {"1.2.3.4", "4.5.6.7"},
					"my-header":       {"something"},
				},
				expectedTags: map[string]any{
					"http.request.headers.x-forwarded-for": "1.2.3.4,4.5.6.7",
				},
			},
			{
				name: "xff-header",
				headers: map[string][]string{
					"X-Forwarded-For": {"1.2.3.4"},
					"my-header":       {"something"},
				},
				expectedTags: map[string]any{
					"http.request.headers.x-forwarded-for": "1.2.3.4",
				},
			},
			{
				name: "no-monitored-headers",
				headers: map[string][]string{
					"my-header": {"something"},
				},
			},
		} {
			for _, respHeadersCase := range []struct {
				name         string
				headers      map[string][]string
				expectedTags map[string]any
			}{
				{
					name: "zero-headers",
				},
				{
					name: "ct-header",
					headers: map[string][]string{
						"Content-Type": {"application/json"},
						"my-header":    {"something"},
					},
					expectedTags: map[string]any{
						"http.response.headers.content-type": "application/json",
					},
				},
				{
					name: "no-monitored-headers",
					headers: map[string][]string{
						"my-header": {"something"},
					},
				},
			} {
				t.Run(fmt.Sprintf("%s-%s-%s", eventCase.name, reqHeadersCase.name, respHeadersCase.name), func(t *testing.T) {
					var span MockSpan
					waf.SetEventSpanTags(&span)
					value, err := json.Marshal(map[string][]any{"triggers": eventCase.events})
					if eventCase.expectedError {
						require.Error(t, err)
						return
					}

					span.SetTag("_dd.appsec.json", string(value))

					require.NoError(t, err)
					setRequestHeadersTags(&span, reqHeadersCase.headers)
					setResponseHeadersTags(&span, respHeadersCase.headers)

					if eventCase.events != nil {
						require.Subset(t, span.Tags, map[string]any{
							"_dd.appsec.json": eventCase.expectedTag,
							"appsec.event":    true,
							"_dd.origin":      "appsec",
							"_dd.p.ts":        internal.TraceSourceTagValue{Value: internal.ASMTraceSource},
						})
					}

					if l := len(reqHeadersCase.expectedTags); l > 0 {
						require.Subset(t, span.Tags, reqHeadersCase.expectedTags)
					}

					if l := len(respHeadersCase.expectedTags); l > 0 {
						require.Subset(t, span.Tags, respHeadersCase.expectedTags)
					}
				})
			}
		}
	}
}

func TestSetRequestHeadersTagsDoesNotOwnSecurityTestingHeaders(t *testing.T) {
	var span MockSpan
	setRequestHeadersTags(&span, map[string][]string{
		"X-Datadog-Endpoint-Scan": {"scan-uuid"},
		"X-Datadog-Security-Test": {"test-uuid"},
		"X-Forwarded-For":         {"1.2.3.4"},
	})

	require.Equal(t, "1.2.3.4", span.Tags["http.request.headers.x-forwarded-for"])
	require.NotContains(t, span.Tags, "http.request.headers.x-datadog-endpoint-scan")
	require.NotContains(t, span.Tags, "http.request.headers.x-datadog-security-test")
}

func TestSecurityTestingHeaderTagValues(t *testing.T) {
	headers := http.Header{
		"X-Datadog-Endpoint-Scan": {" scan-uuid "},
		"X-Datadog-Security-Test": {"test-uuid", "second-value"},
	}

	tagNames, tagValues, count := SecurityTestingHeaderTagValues(headers)

	require.Equal(t, 2, count)
	require.Equal(t, securityTestingEndpointScanTag, tagNames[0])
	require.Equal(t, "scan-uuid", tagValues[0])
	require.Equal(t, securityTestingTag, tagNames[1])
	require.Equal(t, "test-uuid,second-value", tagValues[1])
}

func TestSecurityTestingHeaderByteTagValues(t *testing.T) {
	values := [][2][]byte{
		{[]byte("X-Datadog-Endpoint-Scan"), []byte(" scan-uuid ")},
		{[]byte("X-Datadog-Security-Test"), []byte("test-uuid")},
		{[]byte("x-datadog-security-test"), []byte("second-value")},
	}

	tagNames, tagValues, count := SecurityTestingHeaderByteTagValues(func(visit func(key, value []byte)) {
		for _, value := range values {
			key := append([]byte(nil), value[0]...)
			headerValue := append([]byte(nil), value[1]...)
			visit(key, headerValue)
			for i := range key {
				key[i] = 'x'
			}
			for i := range headerValue {
				headerValue[i] = 'x'
			}
		}
	})

	require.Equal(t, 2, count)
	require.Equal(t, securityTestingEndpointScanTag, tagNames[0])
	require.Equal(t, "scan-uuid", tagValues[0])
	require.Equal(t, securityTestingTag, tagNames[1])
	require.Equal(t, "test-uuid,second-value", tagValues[1])
}

//go:embed testdata/trace_tagging_rules.json
var wafRulesJSON []byte

func TestTraceTagging(t *testing.T) {
	if usable, err := libddwaf.Usable(); !usable {
		t.Skipf("libddwaf is not usable in this context: %v", err)
	}

	wafManager, err := config.NewWAFManagerWithStaticRules(config.ObfuscatorConfig{}, wafRulesJSON)
	require.NoError(t, err)
	defer wafManager.Close()
	cfg := config.Config{
		WAFManager:          wafManager,
		WAFTimeout:          time.Hour,
		TraceRateLimit:      1_000,
		APISec:              config.APISecConfig{Enabled: true, Sampler: apisec.NewSampler(0)},
		RC:                  nil,
		RASP:                false,
		SupportedAddresses:  config.NewAddressSet([]string{"server.request.headers.no_cookies"}),
		MetaStructAvailable: true,
		BlockingUnavailable: false,
		TracingAsTransport:  false,
	}

	rootOp := dyngo.NewRootOperation()
	feat, err := waf.NewWAFFeature(&cfg, rootOp)
	require.NoError(t, err)
	defer feat.Stop()

	feat, err = NewHTTPSecFeature(&cfg, rootOp)
	require.NoError(t, err)
	defer feat.Stop()

	type testCase struct {
		UserAgent    string
		ExpectedTags map[string]any
	}
	testCases := map[string]testCase{
		"Attributes, No Keep, No Event": {
			UserAgent: "TraceTagging/v1+test",
			ExpectedTags: map[string]any{
				"_dd.appsec.trace.integer": int64(662607015),
				"_dd.appsec.trace.agent":   "TraceTagging/v1+test",
			},
		},
		"Attributes, Keep, No Event": {
			UserAgent: "TraceTagging/v2+test",
			ExpectedTags: map[string]any{
				ext.ManualKeep:             true,
				"_dd.appsec.trace.integer": int64(602214076),
				"_dd.appsec.trace.agent":   "TraceTagging/v2+test",
			},
		},
		"Attributes, Keep, Event": {
			UserAgent: "TraceTagging/v3+test",
			ExpectedTags: map[string]any{
				ext.ManualKeep:             true,
				"appsec.event":             true,
				"_dd.appsec.trace.integer": int64(299792458),
				"_dd.appsec.trace.agent":   "TraceTagging/v3+test",
			},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ctx = dyngo.RegisterOperation(ctx, rootOp)

			var span MockSpan
			op, _, _ := httpsec.StartOperation(ctx, httpsec.HandlerOperationArgs{
				Framework:    "test/phony",
				Method:       "GET",
				RequestURI:   "/fake/test/uri",
				RequestRoute: "/fake/:id/uri",
				Host:         "localhost",
				RemoteAddr:   "127.0.0.1:4242",
				ClientIP:     netip.MustParseAddr("127.0.0.1"),
				Headers:      map[string][]string{"user-agent": {tc.UserAgent}},
				Cookies:      map[string][]string{},
				QueryParams:  map[string][]string{},
				PathParams:   map[string]string{"id": "test"},
			}, &span)
			op.Finish(httpsec.HandlerOperationRes{StatusCode: 200})

			require.Subset(t, span.Tags, tc.ExpectedTags)
		})
	}
}

// BenchmarkSecurityTestingHeaderTagValues measures the hot-path cost of
// scanning for security testing headers on every HTTP request.
// The "absent" case represents real user traffic (headers never present);
// the "present" case represents Datadog scan/test traffic.
func BenchmarkSecurityTestingHeaderTagValues(b *testing.B) {
	// Realistic ~20-header request without security testing headers.
	baseHeaders := http.Header{
		"Accept":            {"text/html,application/xhtml+xml"},
		"Accept-Encoding":   {"gzip, deflate, br"},
		"Accept-Language":   {"en-US,en;q=0.9"},
		"Cache-Control":     {"no-cache"},
		"Connection":        {"keep-alive"},
		"Content-Type":      {"application/json"},
		"Host":              {"example.com"},
		"Pragma":            {"no-cache"},
		"Referer":           {"https://example.com/"},
		"User-Agent":        {"Mozilla/5.0 (compatible; BenchmarkBot/1.0)"},
		"X-Forwarded-For":   {"1.2.3.4"},
		"X-Real-Ip":         {"1.2.3.4"},
		"X-Request-Id":      {"abc-123-def"},
		"X-Forwarded-Host":  {"example.com"},
		"X-Forwarded-Port":  {"443"},
		"X-Forwarded-Proto": {"https"},
		"X-Amzn-Trace-Id":   {"Root=1-abc-def"},
		"Cf-Ray":            {"abc123-LAX"},
		"Cf-Connecting-Ip":  {"1.2.3.4"},
		"Via":               {"1.1 proxy.example.com"},
	}

	headersPresent := make(http.Header, len(baseHeaders)+2)
	maps.Copy(headersPresent, baseHeaders)
	headersPresent["X-Datadog-Endpoint-Scan"] = []string{"scan-uuid"}
	headersPresent["X-Datadog-Security-Test"] = []string{"test-uuid"}

	cases := []struct {
		name    string
		headers http.Header
	}{
		{"absent", baseHeaders},
		{"present", headersPresent},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				SecurityTestingHeaderTagValues(tc.headers)
			}
		})
	}
}

func TestHeadersRemoveCookies(t *testing.T) {
	h := http.Header{
		"Cookie":       []string{"session=secret"},
		"Set-Cookie":   []string{"session=secret; HttpOnly"},
		"Content-Type": []string{"application/json"},
	}
	out := headersRemoveCookies(h)
	if _, ok := out["cookie"]; ok {
		t.Fatalf("request cookie header must be excluded from no_cookies address")
	}
	if _, ok := out["set-cookie"]; ok {
		t.Fatalf("response set-cookie header must be excluded from no_cookies address")
	}
	if _, ok := out["content-type"]; !ok {
		t.Fatalf("non-cookie headers must be preserved")
	}
}

// TestSSRFOnStartNilMetricsNoPanic reproduces the nil-metrics dereference in the SSRF listener's
// OnStart: when the WAF handle is unavailable (no context, no metrics set), an outbound request still
// reaches the listener, which must not panic.
func TestSSRFOnStartNilMetricsNoPanic(t *testing.T) {
	// Context operation with NO metrics instance and NO WAF context (mirrors the handle==nil path,
	// where listener/waf.onStart returns before SetMetricsInstance).
	ctxOp, _ := wafemitter.StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	t.Cleanup(ctxOp.Finish)
	require.Nil(t, ctxOp.GetMetricsInstance(), "precondition: metrics must be nil")

	handlerOp := &httpsec.HandlerOperation{
		Operation:        dyngo.NewOperation(ctxOp),
		ContextOperation: ctxOp,
	}
	rtOp := &httpsec.RoundTripOperation{
		Operation:           dyngo.NewOperation(handlerOp),
		SubcontextOperation: handlerOp.NewSubcontextOp(),
		HandlerOp:           handlerOp,
	}

	feature := &DownwardRequestFeature{}
	require.NotPanics(t, func() {
		feature.OnStart(rtOp, httpsec.RoundTripOperationArgs{URL: "http://example.com", Method: "GET"})
	})
}

func TestNormalizeRoute(t *testing.T) {
	const (
		chi    = "github.com/go-chi/chi/v5"
		mux    = "github.com/gorilla/mux"
		gin    = "github.com/gin-gonic/gin"
		echo   = "github.com/labstack/echo/v4"
		fiber  = "github.com/gofiber/fiber/v2"
		netmux = "net/http"
	)
	for _, tc := range []struct {
		name       string
		framework  string
		route      string
		params     map[string]string
		requestURI string
		want       string // empty: no tag
	}{
		// RFC-1125 test cases, in the grammar of a Go framework.
		{name: "1 static", framework: chi, route: "/users", want: "/users"},
		{name: "2 root", framework: chi, route: "/", want: "/"},
		{name: "3 param", framework: chi, route: "/users/{id}", want: "/users/{id}"},
		{name: "4 params", framework: gin, route: "/users/:id/posts/:post_id", want: "/users/{id}/posts/{post_id}"},
		{name: "5 trailing slash", framework: echo, route: "/users/:id/", want: "/users/{id}/"},
		{name: "6 static before param", framework: chi, route: "/users/user-{id}", want: "/users/user-{id}"},
		{name: "7 static before param", framework: gin, route: "/v:version/items", want: "/v{version}/items"},
		{name: "8 static after param", framework: mux, route: "/files/{name}.json", want: "/files/{name}.json"},
		{name: "9 non-ASCII static", framework: chi, route: "/café", want: "/caf%C3%A9"},
		{name: "10 colon in static", framework: chi, route: "/user:me", want: "/user%3Ame"},
		{name: "11 plus in static", framework: mux, route: "/a+b/{id}", want: "/a%2Bb/{id}"},
		{name: "12 params in a segment", framework: chi, route: "/photos/{id}.{format}", want: "/photos/{id}.{format}"},
		{name: "13 three params in a segment", framework: fiber, route: "/d/:a-:b-:c", want: "/d/{a}-{b}-{c}"},
		{name: "14 adjacent params", framework: mux, route: "/v/{major}{minor}", want: "/v/{major}{minor}"},
		{name: "15 optional present", framework: fiber, route: "/users/:id/:tab?", params: map[string]string{"id": "1", "tab": "posts"}, requestURI: "/users/1/posts", want: "/users/{id}/{tab}"},
		{name: "16 optional absent", framework: fiber, route: "/users/:id/:tab?", params: map[string]string{"id": "1", "tab": ""}, requestURI: "/users/1", want: "/users/{id}"},
		{name: "21 optionals absent", framework: fiber, route: "/a/:x?/:y?", params: map[string]string{"x": "", "y": ""}, requestURI: "/a", want: "/a"},
		{name: "22 first optional present", framework: fiber, route: "/a/:x?/:y?", params: map[string]string{"x": "1", "y": ""}, requestURI: "/a/1", want: "/a/{x}"},
		{name: "23 optionals present", framework: fiber, route: "/a/:x?/:y?", params: map[string]string{"x": "1", "y": "2"}, requestURI: "/a/1/2", want: "/a/{x}/{y}"},
		{name: "28 optional unknown", framework: fiber, route: "/users/:id/:tab?", requestURI: "/users/1/posts", want: "/users/{id}"},
		{name: "30 name with dash", framework: chi, route: "/users/{user-id}", want: "/users/{user_id}"},
		{name: "31 name with dot", framework: mux, route: "/items/{item.id}", want: "/items/{item_id}"},
		{name: "32 name collision", framework: gin, route: "/a/:user-id/:user_id", want: "/a/{user_id_2}/{user_id}"},
		{name: "33 name collision, suffix used", framework: chi, route: "/a/{x-y}/{x_y}/{x_y_2}", want: "/a/{x_y_3}/{x_y}/{x_y_2}"},
		{name: "34 non-ASCII name", framework: chi, route: "/u/{föo}", want: "/u/{f_o}"},
		{name: "35 named catch-all", framework: gin, route: "/files/*path", params: map[string]string{"path": "/a/b/c.txt"}, want: "/files/{path}"},
		{name: "36 unnamed catch-all", framework: chi, route: "/static/*", params: map[string]string{"*": "css/app.css"}, want: "/static/{param1}"},
		{name: "37 unnamed catch-all, name used", framework: echo, route: "/w/:param1/*", params: map[string]string{"param1": "x", "*": "a/b"}, want: "/w/{param1}/{param2}"},
		{name: "46 gin param name", framework: gin, route: "/files/:name.json", want: "/files/{name_json}"},
		{name: "47 prefix route", framework: netmux, route: "/static/", requestURI: "/static/css/app.css", want: "/static/{param1}"},
		{name: "48 prefix route, empty remainder", framework: netmux, route: "/static/", requestURI: "/static/", want: "/static/"},
		{name: "49 empty catch-all", framework: netmux, route: "/files/{path...}", params: map[string]string{"path": ""}, want: "/files/"},

		// Regex constraints contribute nothing.
		{name: "regex", framework: chi, route: "/r/{name:report-[a-z]+}", want: "/r/{name}"},
		{name: "regex matching empty", framework: mux, route: "/r/{f:(?:\\.json)?}", want: "/r/{f}"},
		{name: "regex with braces", framework: chi, route: "/r/{id:[0-9]{3}}/x", want: "/r/{id}/x"},
		{name: "gorilla catch-all regex", framework: mux, route: "/r/{path:.*}", want: "/r/{path}"},
		{name: "fiber constraint", framework: fiber, route: "/r/:id<int>", want: "/r/{id}"},

		// Catch-alls.
		{name: "chi empty catch-all", framework: chi, route: "/files/*", params: map[string]string{"*": "/"}, want: "/files/"},
		{name: "chi catch-all unknown", framework: chi, route: "/files/*", want: "/files/"},
		{name: "gin empty catch-all", framework: gin, route: "/files/*path", params: map[string]string{"path": "//"}, want: "/files/"},
		{name: "echo ignores the rest", framework: echo, route: "/a/*/b", params: map[string]string{"*": "x/b"}, want: "/a/{param1}"},
		{name: "fiber wildcard", framework: fiber, route: "/static/*", params: map[string]string{"*1": "css/app.css"}, requestURI: "/static/css/app.css", want: "/static/{param1}"},
		{name: "fiber empty wildcard, slash", framework: fiber, route: "/static/*", params: map[string]string{"*1": ""}, requestURI: "/static/", want: "/static/"},
		{name: "fiber empty wildcard, no slash", framework: fiber, route: "/static/*", params: map[string]string{"*1": ""}, requestURI: "/static", want: "/static"},
		{name: "fiber plus", framework: fiber, route: "/static/+", params: map[string]string{"+1": "a"}, want: "/static/{param1}"},
		{name: "fiber dot is not optional", framework: fiber, route: "/f/:name.:ext?", params: map[string]string{"name": "a", "ext": ""}, want: "/f/{name}."},
		{name: "fiber escape", framework: fiber, route: "/a\\:b/:id", want: "/a%3Ab/{id}"},
		{name: "fiber root", framework: fiber, route: "/", want: "/"},

		// net/http patterns.
		{name: "servemux host", framework: netmux, route: "example.com/foo/{id}", want: "/foo/{id}"},
		{name: "servemux method", framework: netmux, route: "GET /foo/{id}", want: "/foo/{id}"},
		{name: "servemux anchor", framework: netmux, route: "/users/{$}", want: "/users/"},
		{name: "servemux root", framework: netmux, route: "/", requestURI: "/", want: "/"},
		{name: "servemux root prefix", framework: netmux, route: "/", requestURI: "/foo?a=b", want: "/{param1}"},
		{name: "servemux prefix, unknown path", framework: netmux, route: "/static/", want: "/static/"},
		{name: "servemux prefix with param", framework: netmux, route: "/u/{id}/", requestURI: "/u/1/x", want: "/u/{id}/{param1}"},
		{name: "servemux catch-all", framework: netmux, route: "/files/{path...}", params: map[string]string{"path": "a/b"}, want: "/files/{path}"},
		{name: "servemux escape", framework: netmux, route: "/caf%c3%a9", want: "/caf%C3%A9"},
		{name: "servemux invalid escape", framework: netmux, route: "/100%", want: "/100%25"},
		{name: "servemux absolute request URI", framework: netmux, route: "/static/", requestURI: "http://example.com/static/a", want: "/static/{param1}"},
		{name: "servemux partial wildcard", framework: netmux, route: "/users/user-{id}"},

		// % is static text when the framework treats it literally.
		{name: "gin percent", framework: gin, route: "/a%41", want: "/a%2541"},

		// No tag.
		{name: "unknown framework", framework: "github.com/julienschmidt/httprouter", route: "/users/:id"},
		{name: "no leading slash", framework: chi, route: "users"},
		{name: "unbalanced brace", framework: chi, route: "/users/{id"},
		{name: "unnamed param", framework: gin, route: "/users/:"},
		{name: "empty segment", framework: chi, route: "/a//b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeRoute(tc.framework, tc.route, tc.params, tc.requestURI)
			require.Equal(t, tc.want != "", ok, "normalized route: %q", got)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNormalizedRouteTag(t *testing.T) {
	if usable, err := libddwaf.Usable(); !usable {
		t.Skipf("libddwaf is not usable in this context: %v", err)
	}

	wafManager, err := config.NewWAFManagerWithStaticRules(config.ObfuscatorConfig{}, wafRulesJSON)
	require.NoError(t, err)
	defer wafManager.Close()

	start := func(t *testing.T, apiSec bool, args httpsec.HandlerOperationArgs) (*httpsec.HandlerOperation, *MockSpan, context.Context) {
		cfg := config.Config{
			WAFManager:         wafManager,
			WAFTimeout:         time.Hour,
			TraceRateLimit:     1_000,
			APISec:             config.APISecConfig{Enabled: apiSec, Sampler: apisec.NewSampler(0)},
			SupportedAddresses: config.NewAddressSet([]string{"server.request.headers.no_cookies"}),
		}
		rootOp := dyngo.NewRootOperation()
		wafFeat, err := waf.NewWAFFeature(&cfg, rootOp)
		require.NoError(t, err)
		t.Cleanup(wafFeat.Stop)
		httpFeat, err := NewHTTPSecFeature(&cfg, rootOp)
		require.NoError(t, err)
		t.Cleanup(httpFeat.Stop)

		var span MockSpan
		op, _, ctx := httpsec.StartOperation(dyngo.RegisterOperation(context.Background(), rootOp), args, &span)
		return op, &span, ctx
	}

	t.Run("matched route", func(t *testing.T) {
		op, span, _ := start(t, true, httpsec.HandlerOperationArgs{
			Framework:    "github.com/gin-gonic/gin",
			RequestURI:   "/users/42",
			RequestRoute: "/users/:id",
			PathParams:   map[string]string{"id": "42"},
		})
		op.Finish(httpsec.HandlerOperationRes{StatusCode: 200})
		require.Equal(t, "/users/{id}", span.Tags[normalizedRouteTag])
	})

	t.Run("late route and parameters", func(t *testing.T) {
		op, span, ctx := start(t, true, httpsec.HandlerOperationArgs{
			Framework:     "net/http",
			RequestURI:    "/files/a/b",
			RequestRoute:  "/files/{param:str}/{param:str}",
			RouteInferred: true,
		})
		require.NoError(t, httpsec.RouteMatched(ctx, "/files/{path...}", map[string]string{"path": "a/b"}))
		op.Finish(httpsec.HandlerOperationRes{StatusCode: 200})
		require.Equal(t, "/files/{path}", span.Tags[normalizedRouteTag])
	})

	t.Run("inferred route", func(t *testing.T) {
		op, span, _ := start(t, true, httpsec.HandlerOperationArgs{
			Framework:     "net/http",
			RequestURI:    "/users/42",
			RequestRoute:  "/users",
			RouteInferred: true,
		})
		op.Finish(httpsec.HandlerOperationRes{StatusCode: 200})
		require.NotContains(t, span.Tags, normalizedRouteTag)
	})

	t.Run("no route", func(t *testing.T) {
		op, span, _ := start(t, true, httpsec.HandlerOperationArgs{
			Framework:  "github.com/gin-gonic/gin",
			RequestURI: "/users/42",
		})
		op.Finish(httpsec.HandlerOperationRes{StatusCode: 404})
		require.NotContains(t, span.Tags, normalizedRouteTag)
	})

	t.Run("api security disabled", func(t *testing.T) {
		op, span, _ := start(t, false, httpsec.HandlerOperationArgs{
			Framework:    "github.com/gin-gonic/gin",
			RequestURI:   "/users/42",
			RequestRoute: "/users/:id",
		})
		op.Finish(httpsec.HandlerOperationRes{StatusCode: 200})
		require.NotContains(t, span.Tags, normalizedRouteTag)
	})
}
