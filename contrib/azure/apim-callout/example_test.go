// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package apimcallout_test

import (
	"context"
	"log"
	"net/http"

	apimcallout "github.com/DataDog/dd-trace-go/contrib/azure/apim-callout/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func Example() {
	tracer.Start(tracer.WithAppSecEnabled(true))
	defer tracer.Stop()

	handler := apimcallout.NewHandler(apimcallout.AppsecAPIMConfig{
		Context: context.Background(),
	})

	log.Fatal(http.ListenAndServe(":8080", handler))
}
