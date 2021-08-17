// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kubernetes_test

import (
	"fmt"

	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"

	kubernetestrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/k8s.io/client-go/kubernetes"
)

func Example() {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// Use this to trace all calls made to the Kubernetes API
	cfg.WrapTransport = kubernetestrace.WrapRoundTripper

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err.Error())
	}

	pods, err := client.CoreV1().Pods("default").List(meta_v1.ListOptions{})
	if err != nil {
		panic(err)
	}

	fmt.Println(pods.Items)
}
