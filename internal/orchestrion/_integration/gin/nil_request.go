// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseNilRequest struct {
	ctx      *gin.Context
	recorder *httptest.ResponseRecorder
}

func (tc *TestCaseNilRequest) Setup(_ context.Context, t *testing.T) {
	tc.recorder = httptest.NewRecorder()
	tc.ctx, _ = gin.CreateTestContext(tc.recorder)
	require.Nil(t, tc.ctx.Request)
}

func (tc *TestCaseNilRequest) Run(_ context.Context, t *testing.T) {
	require.NotPanics(t, func() {
		tc.ctx.JSON(http.StatusCreated, gin.H{"status": "ok"})
	})
	require.Equal(t, http.StatusCreated, tc.recorder.Code)
}

func (*TestCaseNilRequest) ExpectedTraces() trace.Traces {
	return nil
}
