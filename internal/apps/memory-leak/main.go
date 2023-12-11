// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// memory-leak implements a http service that provides endpoints that can leak
// memory in various interesting ways.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/DataDog/dd-trace-go/internal/apps"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)

var rawJSONData []byte

// init initializes rawJSONData with a serialized JSON object.
//
// TODO(fg): add a note on how big this object is memory to allow estimating the
// amount of leaked memory per request.
func init() {
	const keysCount = 1000
	jsonData := make(map[string]int)
	for i := 0; i < keysCount; i++ {
		jsonData[fmt.Sprintf("key-%d", i)] = i
	}
	rawJSONData, _ = json.Marshal(jsonData)
}

func main() {
	// Start app
	app := apps.Config{}
	app.RunHTTP(func() http.Handler {
		// Setup http routes
		mux := httptrace.NewServeMux()
		// Endpoint and handler names are chosen so we don't give away what they
		// do. The profiling product should make it easy to figure out what the
		// problem is.
		mux.HandleFunc("/lorem", LoremHandler) // Don't leak anything
		mux.HandleFunc("/ipsum", IpsumHandler) // Leak a goroutine stack via a goroutine.
		mux.HandleFunc("/dolor", DolorHandler) // Leak a heap pointer via a global variable.
		mux.HandleFunc("/sit", SitHandler)     // Leak a goroutine stack and a heap pointer via a goroutine.
		// TODO: file leak, cgo heap leak, cgo thread leak, etc.
		return mux
	})
}

func LoremHandler(w http.ResponseWriter, _ *http.Request) {
	parseRequest()
}

func IpsumHandler(w http.ResponseWriter, _ *http.Request) {
	parseRequest()
	go func() {
		<-make(chan struct{}) // block forever
	}()
}

func DolorHandler(w http.ResponseWriter, _ *http.Request) {
	data := parseRequest()
	global.Add(data)
}

func SitHandler(w http.ResponseWriter, _ *http.Request) {
	data := parseRequest()
	go func() {
		<-make(chan struct{}) // block forever
		fmt.Fprint(w, data)   // never executed
	}()
}

var global = sink[any]{} // global variable to leak heap pointers

type sink[T any] struct {
	mu    sync.Mutex
	items []T
}

func (l *sink[T]) Add(item T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, item)
}

// parseRequest pretends to parse the body of a request payload and returns the
// result.
func parseRequest() any {
	var results []any
	// parse the request body more than once to create some allocation and live
	// heap memory that is not actually being leaked.
	const marshalCount = 100
	for i := 0; i < marshalCount; i++ {
		var result any
		json.Unmarshal(rawJSONData, &result)
		results = append(results, result)
	}
	return results[0]
}
