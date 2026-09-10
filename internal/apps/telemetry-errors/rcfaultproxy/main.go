// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Command rcfaultproxy is a reverse proxy that injects a malformed remote-config
// poll response, to dogfood internal/remoteconfig/remoteconfig.go's
// updateState "could not parse the json response body" LogAndReportError site
// (ddtrace/tracer/AGENTS.md dogfooding process, internal/apps/telemetry-errors).
//
// Behavior:
//
//   - Requests to /v0.7/config get a 200 response with a body that is neither
//     valid JSON nor one of the two literals ("{}" / "null") the RC client
//     special-cases as "nothing to do" — guaranteeing json.Unmarshal fails.
//   - Every other request (notably /info and /telemetry/proxy/*) is reverse
//     proxied unmodified to -upstream, a real, already-verified agent. This
//     keeps telemetry reporting itself on the normal, working path — only the
//     RC poll response is faulty.
//
// The tracer only starts its remote-config client if the agent it detects (via
// /info) advertises RC support, so -upstream must be a real agent whose /info
// response includes "/v0.7/config" in its endpoints list.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func main() {
	listen := flag.String("listen", "", "address to listen on, e.g. :20000")
	upstream := flag.String("upstream", "", "real agent base URL to proxy everything else to, e.g. http://localhost:18126")
	flag.Parse()

	if *listen == "" || *upstream == "" {
		log.Fatal("both -listen and -upstream are required")
	}

	target, err := url.Parse(*upstream)
	if err != nil {
		log.Fatalf("invalid -upstream: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	mux := http.NewServeMux()
	mux.HandleFunc("/v0.7/config", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Deliberately malformed: valid HTTP/200, invalid JSON, and not one of
		// the "{}" / "null" literals the RC client treats as a no-op response.
		_, _ = w.Write([]byte(`{"targets": this is not valid json`))
		log.Printf("served malformed /v0.7/config response")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})

	log.Printf("rcfaultproxy listening on %s, forwarding non-RC traffic to %s", *listen, *upstream)
	log.Fatal(http.ListenAndServe(*listen, mux))
}
