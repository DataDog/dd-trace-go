// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package k8sclientgo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type base struct {
	server    *httptest.Server
	serverURL *url.URL
	client    *kubernetes.Clientset
}

func (b *base) setup(_ context.Context, t *testing.T) {
	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Hello World"))
	}))
	t.Cleanup(func() { b.server.Close() })
	tsURL, err := url.Parse(b.server.URL)
	require.NoError(t, err)
	b.serverURL = tsURL
}

func (b *base) run(ctx context.Context, t *testing.T) {
	// TODO(darccio): check if this can be change to nil instead of metav1.ListOptions
	_, err := b.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})

	// we should get an error here since our test server handler implementation doesn't return what the k8s client expects
	require.EqualError(t, err, "serializer for text/plain; charset=utf-8 doesn't exist")
}

func (b *base) expectedTraces() trace.Traces {
	httpServerSpan := &trace.Trace{
		Tags: map[string]any{
			"name":     "http.request",
			"resource": "GET /api/v1/namespaces",
			"type":     "web",
		},
		Meta: map[string]string{
			"component":        "net/http",
			"span.kind":        "server",
			"http.useragent":   rest.DefaultKubernetesUserAgent(),
			"http.status_code": "200",
			"http.host":        b.serverURL.Host,
			"http.url":         fmt.Sprintf("%s/api/v1/namespaces", b.server.URL),
			"http.method":      "GET",
		},
	}
	httpClientSpan := &trace.Trace{
		Tags: map[string]any{
			"name":     "http.request",
			"resource": "GET /api/v1/namespaces",
			"type":     "http",
		},
		Meta: map[string]string{
			"component":                "net/http",
			"span.kind":                "client",
			"network.destination.name": "127.0.0.1",
			"http.status_code":         "200",
			"http.method":              "GET",
			"http.url":                 fmt.Sprintf("%s/api/v1/namespaces", b.server.URL),
		},
		Children: trace.Traces{httpServerSpan},
	}
	k8sClientSpan := &trace.Trace{
		Tags: map[string]any{
			"name":     "http.request",
			"resource": "GET namespaces",
			"type":     "http",
		},
		Meta: map[string]string{
			"component": "k8s.io/client-go/kubernetes",
			"span.kind": "client",
		},
		Children: trace.Traces{httpClientSpan},
	}
	return trace.Traces{k8sClientSpan}
}
