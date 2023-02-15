// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcutil

import (
	"testing"
)

func TestExtractTags(t *testing.T) {
	testCases := []struct {
		Name    string
		Path    string
		Service string
		Method  string
		Package string
	}{
		{"empty test",
			"",
			"",
			"",
			"",
		},
		{"basic test",
			"/mypackage.myservice/mymethod",
			"mypackage.myservice",
			"mymethod",
			"mypackage",
		},
		{"obscure test",
			"/my/p/a/c.k.a.ge.my/se/r/v/ice/myme.t.h.od",
			"my/p/a/c.k.a.ge.my/se/r/v/ice",
			"myme.t.h.od",
			"my/p/a/c.k.a.ge",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			tags := ExtractRPCTags(tc.Path)
			if tags.Service != tc.Service {
				t.Errorf("Service %s != %s", tags.Service, tc.Service)
			} else {
				t.Logf("Service %s == %s", tags.Service, tc.Service)
			}

			if tags.Method != tc.Method {
				t.Errorf("Method %s != %s", tags.Method, tc.Method)
			} else {
				t.Logf("Method %s == %s", tags.Method, tc.Method)
			}

			if tags.Package != tc.Package {
				t.Errorf("Package %s != %s", tags.Package, tc.Package)
			} else {
				t.Logf("Package %s == %s", tags.Package, tc.Package)
			}
		})
	}
}
