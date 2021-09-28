// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kubernetes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	httptrace "gopkg.in/CodapeWild/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/CodapeWild/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestPathToResource(t *testing.T) {
	expected := map[string]string{
		"/api/v1/componentstatuses":                                           "componentstatuses",
		"/api/v1/componentstatuses/NAME":                                      "componentstatuses/{name}",
		"/api/v1/configmaps":                                                  "configmaps",
		"/api/v1/namespaces/default/bindings":                                 "namespaces/{namespace}/bindings",
		"/api/v1/namespaces/someothernamespace/configmaps":                    "namespaces/{namespace}/configmaps",
		"/api/v1/namespaces/default/configmaps/some-config-map":               "namespaces/{namespace}/configmaps/{name}",
		"/api/v1/namespaces/default/persistentvolumeclaims/pvc-abcd/status":   "namespaces/{namespace}/persistentvolumeclaims/{name}/status",
		"/api/v1/namespaces/default/pods/pod-1234/proxy":                      "namespaces/{namespace}/pods/{name}/proxy",
		"/api/v1/namespaces/default/pods/pod-5678/proxy/some-path":            "namespaces/{namespace}/pods/{name}/proxy/{path}",
		"/api/v1/watch/configmaps":                                            "watch/configmaps",
		"/api/v1/watch/namespaces":                                            "watch/namespaces",
		"/api/v1/watch/namespaces/default/configmaps":                         "watch/namespaces/{namespace}/configmaps",
		"/api/v1/watch/namespaces/someothernamespace/configmaps/another-name": "watch/namespaces/{namespace}/configmaps/{name}",
	}

	for path, expectedResource := range expected {
		assert.Equal(t, "GET "+expectedResource, RequestToResource("GET", path), "mapping %v", path)
	}
}

func TestKubernetes(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	cfg, err := clientcmd.BuildConfigFromKubeconfigGetter(s.URL, func() (*clientcmdapi.Config, error) {
		return clientcmdapi.NewConfig(), nil
	})
	assert.NoError(t, err)
	cfg.WrapTransport = WrapRoundTripper

	client, err := kubernetes.NewForConfig(cfg)
	assert.NoError(t, err)

	client.CoreV1().Namespaces().List(meta_v1.ListOptions{})

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	{
		s := spans[0]
		assert.Equal(t, "http.request", s.OperationName())
		assert.Equal(t, "kubernetes", s.Tag(ext.ServiceName))
		assert.Equal(t, "GET namespaces", s.Tag(ext.ResourceName))
		assert.Equal(t, "200", s.Tag(ext.HTTPCode))
		assert.Equal(t, "GET", s.Tag(ext.HTTPMethod))
		assert.Equal(t, "/api/v1/namespaces", s.Tag(ext.HTTPURL))
		auditID, ok := s.Tag("kubernetes.audit_id").(string)
		assert.True(t, ok)
		assert.True(t, len(auditID) > 0)
	}
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...httptrace.RoundTripperOption) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("Hello World"))
		}))
		defer srv.Close()

		cfg, err := clientcmd.BuildConfigFromKubeconfigGetter(srv.URL, func() (*clientcmdapi.Config, error) {
			return clientcmdapi.NewConfig(), nil
		})
		assert.NoError(t, err)
		cfg.WrapTransport = WrapRoundTripperFunc(opts...)

		client, err := kubernetes.NewForConfig(cfg)
		assert.NoError(t, err)

		client.CoreV1().Namespaces().List(meta_v1.ListOptions{})
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)

		s := spans[0]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, httptrace.RTWithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, httptrace.RTWithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, httptrace.RTWithAnalyticsRate(0.23))
	})
}
