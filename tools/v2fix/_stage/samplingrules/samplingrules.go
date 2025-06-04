// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func tags() map[string]string {
	return map[string]string{"hostname": "hn-*", "another": "value"}
}

func main() {
	_ = tracer.ServiceRule("test-service", 1.0)                                                // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
	_ = tracer.NameRule("http.request", 1.0)                                                   // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
	_ = tracer.NameServiceRule("http.request", "test-service", 1.0)                            // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
	_ = tracer.NameServiceRule("http.*", "test-*", 1.0)                                        // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
	_ = tracer.ServiceRule("other-service-1", 0.0)                                             // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
	_ = tracer.ServiceRule("other-service-2", 0.0)                                             // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
	_ = tracer.ServiceRule("test-service", 1.0)                                                // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
	_ = tracer.TagsResourceRule(tags(), "", "", "", 1.0)                                       // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
	_ = tracer.TagsResourceRule(map[string]string{"hostname": "hn-3*"}, "res-1*", "", "", 1.0) // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
	_ = tracer.TagsResourceRule(map[string]string{"hostname": "hn-*"}, "", "", "", 1.0)        // want `a deprecated sampling rule constructor function should be replaced with a tracer.Rule{...} struct literal`
}
