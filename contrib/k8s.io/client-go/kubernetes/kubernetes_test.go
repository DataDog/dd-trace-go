// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kubernetes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/stretchr/testify/assert"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestPathToResource(t *testing.T) {
	expected := map[string]string{
		"/api/v1/componentstatuses":                                                          "componentstatuses",
		"/api/v1/componentstatuses/NAME":                                                     "componentstatuses/{name}",
		"/api/v1/configmaps":                                                                 "configmaps",
		"/api/v1/namespaces/default/bindings":                                                "namespaces/{namespace}/bindings",
		"/api/v1/namespaces/someothernamespace/configmaps":                                   "namespaces/{namespace}/configmaps",
		"/api/v1/namespaces/default/configmaps/some-config-map":                              "namespaces/{namespace}/configmaps/{name}",
		"/api/v1/namespaces/default/persistentvolumeclaims/pvc-abcd/status":                  "namespaces/{namespace}/persistentvolumeclaims/{name}/status",
		"/api/v1/namespaces/default/pods/pod-1234/proxy":                                     "namespaces/{namespace}/pods/{name}/proxy",
		"/api/v1/namespaces/default/pods/pod-5678/proxy/some-path":                           "namespaces/{namespace}/pods/{name}/proxy/{path}",
		"/api/v1/watch/configmaps":                                                           "watch/configmaps",
		"/api/v1/watch/namespaces":                                                           "watch/namespaces",
		"/api/v1/watch/namespaces/default/configmaps":                                        "watch/namespaces/{namespace}/configmaps",
		"/api/v1/watch/namespaces/someothernamespace/configmaps/another-name":                "watch/namespaces/{namespace}/configmaps/{name}",
		"/apis/apps/v1/namespaces/default/replicasets":                                       "apps/v1/namespaces/{namespace}/replicasets",
		"/apis/apps/v1/namespaces/someothernamespace/replicasets":                            "apps/v1/namespaces/{namespace}/replicasets",
		"/apis/apps/v1/namespaces/default/replicasets/foo":                                   "apps/v1/namespaces/{namespace}/replicasets/{name}",
		"/apis/apps/v1/watch/namespaces/default/replicasets":                                 "apps/v1/watch/namespaces/{namespace}/replicasets",
		"/apis/apps/v1/watch/namespaces/someothernamespace/replicasets":                      "apps/v1/watch/namespaces/{namespace}/replicasets",
		"/apis/apps/v1/watch/namespaces/default/replicasets/foo":                             "apps/v1/watch/namespaces/{namespace}/replicasets/{name}",
		"/apis/coordination.k8s.io/v1/namespaces/default/leases":                             "coordination.k8s.io/v1/namespaces/{namespace}/leases",
		"/apis/coordination.k8s.io/v1/namespaces/default/leases/some-lease":                  "coordination.k8s.io/v1/namespaces/{namespace}/leases/{name}",
		"/apis/coordination.k8s.io/v1/namespaces/someothernamespace/leases/some-lease":       "coordination.k8s.io/v1/namespaces/{namespace}/leases/{name}",
		"/apis/coordination.k8s.io/v1/watch/namespaces/default/leases/some-lease":            "coordination.k8s.io/v1/watch/namespaces/{namespace}/leases/{name}",
		"/apis/coordination.k8s.io/v1/watch/namespaces/someothernamespace/leases/some-lease": "coordination.k8s.io/v1/watch/namespaces/{namespace}/leases/{name}",
	}

	for path, expectedResource := range expected {
		assert.Equal(t, "GET "+expectedResource, RequestToResource("GET", path), "mapping %v", path)
	}
}

func TestKubernetes(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	client.CoreV1().Namespaces().List(context.TODO(), meta_v1.ListOptions{})

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	{
		span := spans[0]
		assert.Equal(t, "http.request", span.OperationName())
		assert.Equal(t, "GET namespaces", span.Tag(ext.ResourceName))
		assert.Equal(t, "200", span.Tag(ext.HTTPCode))
		assert.Equal(t, "GET", span.Tag(ext.HTTPMethod))
		assert.Equal(t, s.URL+"/api/v1/namespaces", span.Tag(ext.HTTPURL))
		auditID, ok := span.Tag("kubernetes.audit_id").(string)
		assert.True(t, ok)
		assert.True(t, len(auditID) > 0)
		assert.Equal(t, "k8s.io/client-go/kubernetes", span.Tag(ext.Component))
		assert.Equal(t, componentName, span.Integration())
		assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind))
	}
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...httptrace.RoundTripperOption) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

		client.CoreV1().Namespaces().List(context.TODO(), meta_v1.ListOptions{})
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

		testutils.SetGlobalAnalyticsRate(t, 0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, httptrace.WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, httptrace.WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalAnalyticsRate(t, 0.4)

		assertRate(t, mt, 0.23, httptrace.WithAnalyticsRate(0.23))
	})
}

type passThrough struct{ base http.RoundTripper }

type cloning struct{ base http.RoundTripper }

func (rt cloning) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt.base.RoundTrip(req.Clone(req.Context()))
}

// sideCall sends its own request, built from the context of the one it
// forwards, through other before forwarding.
type sideCall struct {
	base, other http.RoundTripper
	url         string
}

func (rt sideCall) RoundTrip(req *http.Request) (*http.Response, error) {
	side, err := http.NewRequestWithContext(req.Context(), http.MethodGet, rt.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := rt.other.RoundTrip(side)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return rt.base.RoundTrip(req)
}

func (rt passThrough) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt.base.RoundTrip(req)
}

// TestWrapRoundTripperOnce checks that a request gets one span when the contrib
// wraps the transport twice, also with another round tripper in between.
func TestWrapRoundTripperOnce(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer s.Close()

	for name, wrap := range map[string]func(http.RoundTripper) http.RoundTripper{
		"adjacent": func(rt http.RoundTripper) http.RoundTripper {
			return WrapRoundTripper(WrapRoundTripper(rt))
		},
		"separated": func(rt http.RoundTripper) http.RoundTripper {
			return WrapRoundTripper(passThrough{WrapRoundTripper(rt)})
		},
		"cloned": func(rt http.RoundTripper) http.RoundTripper {
			return WrapRoundTripper(cloning{WrapRoundTripper(rt)})
		},
	} {
		t.Run(name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest(http.MethodGet, s.URL+"/api/v1/namespaces", nil)
			assert.NoError(t, err)
			resp, err := wrap(http.DefaultTransport).RoundTrip(req)
			assert.NoError(t, err)
			resp.Body.Close()
			assert.Len(t, mt.FinishedSpans(), 1)
		})
	}
}

// TestWrapRoundTripperTracesSideCalls checks that a separate request built from
// the context of a traced one still gets its own span.
func TestWrapRoundTripperTracesSideCalls(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer s.Close()
	mt := mocktracer.Start()
	defer mt.Stop()

	rt := WrapRoundTripper(sideCall{
		base:  http.DefaultTransport,
		other: WrapRoundTripper(http.DefaultTransport),
		url:   s.URL + "/api/v1/pods",
	})
	req, err := http.NewRequest(http.MethodGet, s.URL+"/api/v1/namespaces", nil)
	assert.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	assert.NoError(t, err)
	resp.Body.Close()
	assert.Len(t, mt.FinishedSpans(), 2)
}
