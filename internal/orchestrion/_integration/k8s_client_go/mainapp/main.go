// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp builds client-go configs in package main, which the harness
// suites never build. TestConfigsInMain builds and runs it.
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	kubernetestrace "github.com/DataDog/dd-trace-go/contrib/k8s.io/client-go/v2/kubernetes"
)

func main() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Hello World"))
	}))
	defer srv.Close()

	list(&rest.Config{Host: srv.URL}, "main-pointer-literal")
	value := rest.Config{Host: srv.URL}
	list(&value, "main-value-literal")
	list(&rest.Config{Host: srv.URL, WrapTransport: kubernetestrace.WrapRoundTripper}, "main-contrib")
	cfg, err := clientcmd.BuildConfigFromKubeconfigGetter(srv.URL, func() (*clientcmdapi.Config, error) {
		return clientcmdapi.NewConfig(), nil
	})
	if err != nil {
		panic(err)
	}
	list(cfg, "main-clientcmd")
	listAliased(srv.URL)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

// list lists the pods of namespace, which names the config in the request URL.
// The test server does not answer like an API server, so the call fails.
func list(cfg *rest.Config, namespace string) {
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}
	_, _ = client.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{})
}
