// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/DataDog/dd-trace-go/internal/apps"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)

var dummyData = map[string]int{}

func init() {
	for i := 0; i < 500; i++ {
		dummyData[fmt.Sprintf("key-%d", i)] = i
	}
}

func main() {
	// Enable unit of work
	os.Setenv("DD_PROFILING_ENDPOINT_COUNT_ENABLED", "true")

	// Start app
	app := apps.Config{}
	app.RunHTTP(func() http.Handler {
		// Setup http routes
		mux := httptrace.NewServeMux()
		mux.HandleFunc("/foo", FooHandler)
		mux.HandleFunc("/bar", BarHandler)
		return mux
	})
}

// FooHandler does twice the amount of cpu work per request as BarHandler.
func FooHandler(w http.ResponseWriter, _ *http.Request) {
	cpuWork(1000)
}

// BarHandler does half the amount of cpu work per request as FooHandler.
func BarHandler(w http.ResponseWriter, _ *http.Request) {
	cpuWork(500)
}

func cpuWork(count int) {
	for i := 0; i < count; i++ {
		json.Marshal(dummyData)
	}
}
