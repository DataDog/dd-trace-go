// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/profiler"
)

func main() {
	os.Setenv("DD_APPSEC_ENABLED", "true")
	tracer.Start(tracer.WithDebugMode(true))
	defer tracer.Stop()
	profiler.Start()
	defer profiler.Stop()

	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, "ok"); err != nil {
			panic(err)
		}
	})

	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		panic(err)
	}
	go http.Serve(l, mux)

	res, err := http.Get("http://localhost:8080")
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	if sc := res.StatusCode; sc != http.StatusOK {
		panic(fmt.Errorf("unexpected status code: %d", sc))
	}
	buf, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}
	if str := string(buf); str != "ok" {
		panic(fmt.Errorf("unexpected response body: %s", str))
	}

	fmt.Println("smoke test passed")
	os.Exit(0)
}
