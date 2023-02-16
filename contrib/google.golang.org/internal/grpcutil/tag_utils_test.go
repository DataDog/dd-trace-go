// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractTags(t *testing.T) {
	testCases := []struct {
		Name       string
		FullMethod string
		Expected   RPCTags
	}{
		{"empty test", "",
			RPCTags{
				Method:  "",
				Service: "",
				Package: "",
			}},
		{"basic test", "/mypackage.myservice/mymethod",
			RPCTags{
				Method:  "mymethod",
				Service: "mypackage.myservice",
				Package: "mypackage",
			}},
		{"obscure test", "/my/p/a/ckage.my/se/r/v/ice/myme.t.h.od",
			RPCTags{
				Method:  "unknown",
				Service: "unknown",
				Package: "unknown",
			}},
		{"no package test", "/myservice/mymethod",
			RPCTags{
				Method:  "mymethod",
				Service: "myservice",
				Package: "",
			}},
	}
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			tags := ExtractRPCTags(tc.FullMethod)
			assert.Equal(t, tc.Expected, tags)
		})
	}
}
