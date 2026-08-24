// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"net/http"

	"github.com/DataDog/dd-trace-go/internal/apps/v2"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
)

func main() {
	app := apps.Config{}
	app.RunHTTP(func() http.Handler {
		mux := httptrace.NewServeMux()
		mux.HandleFunc("/decision-maker", DecisionMakerHandler)
		mux.HandleFunc("/decision-maker-target", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintln(w, "ok")
		})
		return mux
	})
}

// DecisionMakerHandler self-requests /decision-maker-target carrying a
// malformed _dd.p.dm value on x-datadog-tags, the way an upstream service's
// header would arrive over the wire. /decision-maker-target is instrumented
// (httptrace.NewServeMux), so accepting that request runs the header through
// the real extraction path: (*propagator).extractTextMap ->
// unmarshalPropagatingTagsIntoTrace -> replacePropagatingTags ->
// parseDecisionMaker (ddtrace/tracer/propagating_tags.go), which reports the
// parse failure via telemetrylog.LogAndReportError.
func DecisionMakerHandler(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://"+r.Host+"/decision-maker-target", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("x-datadog-trace-id", "1")
	req.Header.Set("x-datadog-parent-id", "1")
	req.Header.Set("x-datadog-tags", "_dd.p.dm=zz")

	// A plain, uninstrumented client: injecting our own well-formed trace
	// context here would overwrite the malformed header set above.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	fmt.Fprintln(w, "triggered decision-maker parse error")
}
