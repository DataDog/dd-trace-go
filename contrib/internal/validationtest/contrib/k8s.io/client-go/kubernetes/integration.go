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

	"github.com/stretchr/testify/assert"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	kubernetestrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/k8s.io/client-go/kubernetes"
)

type Integration struct {
	s        *httptest.Server
	c        *kubernetes.Clientset
	numSpans int
	opts     []httptrace.RoundTripperOption
}

func New() *Integration {
	return &Integration{
		opts: make([]httptrace.RoundTripperOption, 0),
	}
}

func (i *Integration) Name() string {
	return "k8s.io/client-go/kubernetes"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World"))
	}))

	cfg, err := clientcmd.BuildConfigFromKubeconfigGetter(i.s.URL, func() (*clientcmdapi.Config, error) {
		return clientcmdapi.NewConfig(), nil
	})
	assert.NoError(t, err)
	cfg.WrapTransport = kubernetestrace.WrapRoundTripperFunc(i.opts...)

	i.c, err = kubernetes.NewForConfig(cfg)
	assert.NoError(t, err)

	t.Cleanup(func() {
		i.s.Close()
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	i.c.CoreV1().Namespaces().List(context.TODO(), meta_v1.ListOptions{})
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, httptrace.RTWithServiceName(name))
}
