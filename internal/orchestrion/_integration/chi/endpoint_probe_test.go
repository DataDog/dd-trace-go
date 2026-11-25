// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/go-chi/chi"
	"github.com/stretchr/testify/require"
)

func TestEndpointProbe(t *testing.T) {
	rules := `{"version":"2.2","metadata":{"rules_version":"1.0.0"},"rules":[{"id":"path-block","name":"path-block","tags":{"type":"path-test","category":"attack_attempt"},"conditions":[{"operator":"match_regex","parameters":{"inputs":[{"address":"server.request.path_params","key_path":["id"]}],"regex":"attack"}}],"on_match":["block"]}],"actions":[{"id":"block","type":"block_request","parameters":{"status_code":403,"type":"auto"}}]}`
	path := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(path, []byte(rules), 0600))
	t.Setenv("DD_APPSEC_RULES", path)
	t.Setenv("DD_APPSEC_ENABLED", "true")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	testutils.StartAppSec(t)
	calls := 0
	r := chi.NewRouter()
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/attack", nil))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Zero(t, calls)
	require.Len(t, mt.FinishedSpans(), 1)
}
