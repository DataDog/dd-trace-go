// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"sync"

	"github.com/DataDog/dd-trace-go/internal/apps"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)

var rawJSONData []byte
var marshalCount int

func init() {
	jsonData := make(map[string]int)
	for i := 0; i < 1000; i++ {
		jsonData[fmt.Sprintf("key-%d", i)] = i
	}
	rawJSONData, _ = json.Marshal(jsonData)
}

func main() {
	// Setup flags
	flag.IntVar(&marshalCount, "marshalCount", 1000, "Number of times to call json.Marshal in the handlers.")

	// Start app
	app := apps.App{Name: "memory-leak"}
	app.RunHTTP(func() http.Handler {
		// Setup http routes
		mux := httptrace.NewServeMux()
		mux.HandleFunc("/foo", FooHandler)
		mux.HandleFunc("/bar", BarHandler)
		return mux
	})
}

// FooHandler unmashals rawJSONData marshalCount times.
func FooHandler(w http.ResponseWriter, _ *http.Request) {
	for i := 0; i < marshalCount; i++ {
		var jsonData any
		json.Unmarshal(rawJSONData, &jsonData)
	}
}

// leakSink is a global variable that is used to leak memory.
var leakSink struct {
	sync.Mutex
	data []any
}

// BarHandler marshals dummyData marshalCount times and leaks the result.
func BarHandler(w http.ResponseWriter, _ *http.Request) {
	var jsonData any
	for i := 0; i < marshalCount; i++ {
		json.Unmarshal(rawJSONData, &jsonData)
	}

	// t := ticker.NewTicker(1 * time.Second)
	// _ = t
	// Goroutine leak
	go func() {
		<-make(chan struct{})
		// fmt.Printf("jsonData: %v\n", jsonData)
	}()
	// leakSink.Lock()
	// leakSink.data = append(leakSink.data, jsonData)
	// leakSink.Unlock()
}
