// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitHostPort(t *testing.T) {
	for _, tc := range []struct {
		Addr                       string
		ExpectedHost, ExpectedPort string
	}{
		{
			Addr:         "",
			ExpectedHost: "",
			ExpectedPort: "",
		},
		{
			Addr:         ":33",
			ExpectedPort: "33",
		},
		{
			Addr:         "33:",
			ExpectedHost: "33",
		},
		{
			Addr:         "[fe80::1.2.3.4]:33",
			ExpectedHost: "fe80::1.2.3.4",
			ExpectedPort: "33",
		},
		{
			Addr:         "[fe80::1.2.3.4]",
			ExpectedHost: "fe80::1.2.3.4",
		},
		{
			Addr:         " [fe80::1.2.3.4] ",
			ExpectedHost: "fe80::1.2.3.4",
		},
		{
			Addr:         "localhost:80 ",
			ExpectedHost: "localhost",
			ExpectedPort: "80",
		},
	} {
		t.Run(tc.Addr, func(t *testing.T) {
			host, port := splitHostPort(tc.Addr)
			require.Equal(t, tc.ExpectedHost, host)
			require.Equal(t, tc.ExpectedPort, port)
		})
	}
}
