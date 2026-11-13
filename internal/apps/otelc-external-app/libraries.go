// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
	"github.com/julienschmidt/httprouter"
	"github.com/valyala/fasthttp"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"
	mongov2options "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// useLibraries calls libraries whose otelc rules bring their contrib into the
// build. This module imports none of those contribs, so its otelc build fails
// if a rules module stops pulling its contrib in. The integration suites can't
// catch that: their module requires every contrib.
func useLibraries() {
	gin.SetMode(gin.ReleaseMode)
	for _, h := range []http.Handler{gin.New(), mux.NewRouter(), httprouter.New()} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}
	client, server := net.Pipe()
	_ = client.Close()
	_ = fasthttp.ServeConn(server, func(*fasthttp.RequestCtx) {})
	_ = session.Must(session.NewSession(&aws.Config{
		Credentials: credentials.AnonymousCredentials,
		Region:      new("us-east-1"),
	}))
	_ = mongooptions.Client()
	_ = mongov2options.Client()
	_ = slog.New(slog.NewJSONHandler(io.Discard, nil))
}
