package namingschematest

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const (
	testDDService       = "dd-service"
	testServiceOverride = "service-override"
)

type assertSpansFn func(t *testing.T, spans []*mocktracer.Span)

type serviceNameAssertions struct {
	defaults        []string
	ddService       []string
	serviceOverride []string
}

type testCase struct {
	name              instrumentation.Package
	genSpans          func(t *testing.T, serviceOverride string) []*mocktracer.Span
	wantServiceNameV0 serviceNameAssertions
	assertOpV0        assertSpansFn
	assertOpV1        assertSpansFn
}

func TestNamingSchema(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}
	t.Setenv("__DD_TRACE_NAMING_SCHEMA_TEST", "1")

	testCases := []testCase{
		gqlgen,
		awsSDKV1,
		awsSDKV1Messaging,
		awsSDKV2,
		awsSDKV2Messaging,
		netHTTPServer,
		netHTTPClient,
		gomemcache,
	}
	for _, tc := range testCases {
		t.Run(strings.ReplaceAll(string(tc.name), "/", "_"), func(t *testing.T) {
			t.Run("ServiceName", func(t *testing.T) {
				// v0
				t.Run("v0_defaults", func(t *testing.T) {
					t.Setenv("DD_SERVICE", "")
					t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
					spans := tc.genSpans(t, "")
					assertServiceNames(t, spans, tc.wantServiceNameV0.defaults)
				})
				t.Run("v0_dd_service", func(t *testing.T) {
					t.Setenv("DD_SERVICE", testDDService)
					t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
					spans := tc.genSpans(t, "")
					assertServiceNames(t, spans, tc.wantServiceNameV0.ddService)
				})
				t.Run("v0_dd_service_and_override", func(t *testing.T) {
					t.Setenv("DD_SERVICE", testDDService)
					t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
					spans := tc.genSpans(t, testServiceOverride)
					assertServiceNames(t, spans, tc.wantServiceNameV0.serviceOverride)
				})

				// v1
				t.Run("v1_defaults", func(t *testing.T) {
					t.Setenv("DD_SERVICE", "")
					t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
					spans := tc.genSpans(t, "")
					assertServiceNames(t, spans, tc.wantServiceNameV0.defaults)
				})
				t.Run("v1_dd_service", func(t *testing.T) {
					t.Setenv("DD_SERVICE", testDDService)
					t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
					spans := tc.genSpans(t, "")
					assertServiceNames(t, spans, repeatString(testDDService, len(tc.wantServiceNameV0.ddService)))
				})
				t.Run("v1_dd_service_and_override", func(t *testing.T) {
					t.Setenv("DD_SERVICE", testDDService)
					t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
					spans := tc.genSpans(t, testServiceOverride)
					assertServiceNames(t, spans, repeatString(testServiceOverride, len(tc.wantServiceNameV0.serviceOverride)))
				})
			})

			t.Run("SpanName", func(t *testing.T) {
				t.Run("v0", func(t *testing.T) {
					t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
					spans := tc.genSpans(t, "")
					tc.assertOpV0(t, spans)
				})
				t.Run("v1", func(t *testing.T) {
					t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
					spans := tc.genSpans(t, "")
					tc.assertOpV1(t, spans)
				})
			})
		})
	}
}

func assertServiceNames(t *testing.T, spans []*mocktracer.Span, wantServiceNames []string) {
	t.Helper()
	require.Len(t, spans, len(wantServiceNames), "the number of spans and number of assertions should be the same")
	for i := 0; i < len(spans); i++ {
		want, got, spanName := wantServiceNames[i], spans[i].Tag(ext.ServiceName), spans[i].OperationName()
		if want == "" {
			assert.Empty(t, got, "expected empty service name tag for span: %s", spanName)
		} else {
			assert.Equal(t, want, got, "incorrect service name for span: %s", spanName)
		}
	}
}

func repeatString(s string, n int) []string {
	r := make([]string, 0, n)
	for i := 0; i < n; i++ {
		r = append(r, s)
	}
	return r
}
