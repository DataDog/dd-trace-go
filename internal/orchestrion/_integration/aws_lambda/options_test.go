// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awslambda

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

func TestLambdaOptions(t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("requires an orchestrion-built test binary; run via orchestrion go test")
	}
	bin := maintest.BuildWithOrchestrion(t, "./aws_lambda/mainapp")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		variant, payload, response, errorText string
	}{
		{variant: "UseNumber", payload: `{"id":9007199254740993}`, response: `"9007199254740993"`},
		{variant: "DisallowUnknownFields", payload: `{"name":"test","unknown":true}`, errorText: `unknown field`},
		{variant: "PrebuiltContext", payload: `{}`, response: `"base-context/context-value"`},
		{variant: "EncodingOptions", payload: `{}`, response: "{\n  \"value\": \"<response>\"\n}\n"},
	} {
		t.Run(tc.variant, func(t *testing.T) {
			t.Parallel()
			api, stdout, stderr := runLambda(t, bin, tc.variant, tc.payload, srv.URL)
			if tc.errorText != "" {
				require.Lenf(t, api.Errors(), invocations, "stderr:\n%s", stderr)
				for _, err := range api.Errors() {
					assert.Contains(t, err, tc.errorText)
				}
				var roots []span
				for _, s := range parseSpans(t, stdout) {
					if s.Name == "aws.lambda" {
						roots = append(roots, s)
					}
				}
				require.Lenf(t, roots, invocations, "stdout:\n%s", stdout)
				for _, root := range roots {
					assert.Equal(t, 1, root.Error)
					assert.Contains(t, root.Meta["error.message"], tc.errorText)
				}
				return
			}
			require.Emptyf(t, api.Errors(), "stderr:\n%s", stderr)
			require.Equal(t, []string{tc.response, tc.response}, api.Responses())
			requireTraces(t, parseSpans(t, stdout), stdout)
		})
	}
}
