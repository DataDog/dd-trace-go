// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcutil

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
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
			if tags[ext.RPCService] != tc.Service {
				t.Errorf("Service %s != %s", tags[ext.RPCService], tc.Service)
			} else {
				t.Logf("Service %s == %s", tags[ext.RPCService], tc.Service)
			}

			if tags[ext.RPCMethod] != tc.Method {
				t.Errorf("Method %s != %s", tags[ext.RPCMethod], tc.Method)
			} else {
				t.Logf("Method %s == %s", tags[ext.RPCMethod], tc.Method)
			}

			if tags[ext.GRPCPackage] != tc.Package {
				t.Errorf("Package %s != %s", tags[ext.RPCService], tc.Package)
			} else {
				t.Logf("Package %s == %s", tags[ext.RPCService], tc.Package)
			}
		})
	}
}
