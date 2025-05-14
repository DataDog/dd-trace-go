// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gatewayapi_test

import (
	"net/http"

	gatewayapi "github.com/DataDog/dd-trace-go/contrib/k8s.io/gateway-api/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	http.Handle("/", gatewayapi.HTTPRequestMirrorHandler(gatewayapi.Config{}))
	http.ListenAndServe(":8080", nil)
}
