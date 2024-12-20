package httptrace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"

	"github.com/stretchr/testify/assert"
)

var appListener *httptest.Server
var inferredHeaders = map[string]string{
	"x-dd-proxy":                 "aws-apigateway",
	"x-dd-proxy-request-time-ms": "1729780025473",
	"x-dd-proxy-path":            "/test",
	"x-dd-proxy-httpmethod":      "GET",
	"x-dd-proxy-domain-name":     "example.com",
	"x-dd-proxy-stage":           "dev",
}

// mock the aws server
func loadTest(t *testing.T) {
	// Set environment variables
	t.Setenv("DD_SERVICE", "aws-server")
	t.Setenv("DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED", "true")

	// set up http server
	mux := http.NewServeMux()

	// set routes
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"message": "ERROR"})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"message": "OK"})
		}
	})
	appListener = httptest.NewServer(mux)

}
func cleanupTest() {
	// close server
	if appListener != nil {
		appListener.Close()
	}
}

func TestInferredProxySpans(t *testing.T) {

	t.Run("should create parent and child spans for a 200", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		loadTest(t)
		defer cleanupTest()

		client := &http.Client{}
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/", appListener.URL), nil)

		assert := assert.New(t)
		assert.NoError(err)

		for k, v := range inferredHeaders {
			req.Header.Set(k, v)
		}

		sp, _ := StartRequestSpan(req)
		resp, err := client.Do(req)
		FinishRequestSpan(sp, resp.StatusCode, nil)

		spans := mt.FinishedSpans()

		assert.NoError(err)
		assert.Equal(http.StatusOK, resp.StatusCode)

		assert.Equal(2, len(spans))
		gateway_span := spans[0]
		web_req_span := spans[1]
		assert.Equal("aws.apigateway", gateway_span.OperationName())
		assert.Equal("http.request", web_req_span.OperationName())
		assert.True(web_req_span.ParentID() == gateway_span.SpanID())
		for _, arg := range inferredHeaders {
			header, tag := normalizer.HeaderTag(arg)

			// Default to an empty string if the tag does not exist
			gateway_span_tags, exists := gateway_span.Tags()[tag]
			if !exists {
				gateway_span_tags = ""
			}
			expected_tags := strings.Join(req.Header.Values(header), ",")
			// compare expected and actual values
			assert.Equal(expected_tags, gateway_span_tags)
		}

		assert.Equal(2, len(spans))

	})

	t.Run("should create parent and child spans for error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		loadTest(t)
		defer cleanupTest()

		client := &http.Client{}
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/error", appListener.URL), nil)
		assert := assert.New(t)
		assert.NoError(err)
		for k, v := range inferredHeaders {
			req.Header.Set(k, v)
		}

		sp, _ := StartRequestSpan(req)
		resp, err := client.Do(req)
		FinishRequestSpan(sp, resp.StatusCode, nil)

		assert.NoError(err)
		assert.Equal(http.StatusInternalServerError, resp.StatusCode)

		spans := mt.FinishedSpans()
		assert.Equal(2, len(spans))
		gateway_span := spans[0]
		web_req_span := spans[1]
		assert.Equal("aws.apigateway", gateway_span.OperationName())
		assert.Equal("http.request", web_req_span.OperationName())
		assert.True(web_req_span.ParentID() == gateway_span.SpanID())
		for _, arg := range inferredHeaders {
			header, tag := normalizer.HeaderTag(arg)

			// Default to an empty string if the tag does not exist
			gateway_span_tags, exists := gateway_span.Tags()[tag]
			if !exists {
				gateway_span_tags = ""
			}
			expected_tags := strings.Join(req.Header.Values(header), ",")
			// compare expected and actual values
			assert.Equal(expected_tags, gateway_span_tags)
		}
		assert.Equal(2, len(spans))

	})

	t.Run("should not create API Gateway spanif headers are missing", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		loadTest(t)
		defer cleanupTest()

		client := &http.Client{}
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/no-aws-headers", appListener.URL), nil)
		assert := assert.New(t)
		assert.NoError(err)

		sp, _ := StartRequestSpan(req)
		resp, err := client.Do(req)
		FinishRequestSpan(sp, resp.StatusCode, nil)
		assert.NoError(err)
		assert.Equal(http.StatusOK, resp.StatusCode)

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))
		assert.Equal("http.request", spans[0].OperationName())

	})
	t.Run("should not create API Gateway span if x-dd-proxy is missing", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		loadTest(t)
		defer cleanupTest()

		client := &http.Client{}
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/no-aws-headers", appListener.URL), nil)
		assert := assert.New(t)
		assert.NoError(err)

		for k, v := range inferredHeaders {
			if k != "x-dd-proxy" {
				req.Header.Set(k, v)
			}
		}

		sp, _ := StartRequestSpan(req)
		resp, err := client.Do(req)
		FinishRequestSpan(sp, resp.StatusCode, nil)

		assert.NoError(err)
		assert.Equal(http.StatusOK, resp.StatusCode)

		spans := mt.FinishedSpans()
		assert.Equal(1, len(spans))
		assert.Equal("http.request", spans[0].OperationName())

	})
}
