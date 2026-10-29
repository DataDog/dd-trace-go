// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package k8sclientgo

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestConfigsInMain covers client-go configs built in package main, including
// one imported under another name. The harness suites never build a package
// main. Each request must report exactly one client-go span.
func TestConfigsInMain(t *testing.T) {
	agent := maintest.Run(t, "./k8s_client_go/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")

	requests := make(map[string]int)
	for _, span := range lister.Spans() {
		if span.Meta["component"] != "k8s.io/client-go/kubernetes" {
			continue
		}
		path := span.Meta["http.url"]
		path = path[strings.Index(path, "/api/"):]
		requests[path]++
	}
	for _, namespace := range []string{
		"main-pointer-literal",
		"main-value-literal",
		"main-contrib",
		"main-clientcmd",
		"main-alias",
	} {
		assert.Equalf(t, 1, requests["/api/v1/namespaces/"+namespace+"/pods"], "client-go spans for %s", namespace)
	}
}
