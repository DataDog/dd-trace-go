// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http_test

import (
	"net/http"
	"net/http/httptest"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)

func Example() {
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	http.ListenAndServe(":8080", mux)
}

func ExampleWAF() {
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+"/?attack=<script>alert()</script>", nil)
	if err != nil {
		panic(err)
	}
	res, err := srv.Client().Do(req)
	_, _ = res, err
	// Output:
}

func Example_withServiceName() {
	mux := httptrace.NewServeMux(httptrace.WithServiceName("my-service"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	http.ListenAndServe(":8080", mux)
}
