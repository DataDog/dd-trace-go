// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"

	pubsubv1api "cloud.google.com/go/pubsub/apiv1"
	pubsubv2api "cloud.google.com/go/pubsub/v2/apiv1"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
	"github.com/julienschmidt/httprouter"
	"github.com/valyala/fasthttp"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"
	mongov2options "go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	// gRPC connects lazily, so nothing dials this endpoint.
	pubsubOpts := []option.ClientOption{
		option.WithEndpoint("localhost:1"),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		option.WithoutAuthentication(),
	}
	if c, err := pubsubv1api.NewPublisherClient(context.Background(), pubsubOpts...); err == nil {
		_ = c.Close()
	}
	if c, err := pubsubv2api.NewTopicAdminClient(context.Background(), pubsubOpts...); err == nil {
		_ = c.Close()
	}
}
