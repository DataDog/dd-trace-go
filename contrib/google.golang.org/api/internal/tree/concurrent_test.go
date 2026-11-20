// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package tree

import (
	"sync"
	"testing"
)

// The endpoint tree is a process global and Get runs on every traced request,
// so two requests that first match the same endpoint must not race while the
// path matcher is prepared.
func TestConcurrentGetNoRace(t *testing.T) {
	e := &Endpoint{
		Hostname:     "example.com",
		HTTPMethod:   "GET",
		PathTemplate: "/foo/{id}",
		PathRegex:    "^/foo/[^/]+$",
		ServiceName:  "svc",
		ResourceName: "res",
	}
	tr, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			ep, ok := tr.Get("example.com", "GET", "/foo/123")
			if !ok || ep.ResourceName != "res" {
				t.Errorf("unexpected match: ok=%v ep=%+v", ok, ep)
			}
		})
	}
	wg.Wait()
}
