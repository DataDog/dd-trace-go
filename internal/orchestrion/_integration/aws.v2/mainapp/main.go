// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp builds aws.Config values in package main, where only the
// otelc rules with a target matching main apply, and calls DynamoDB with each.
// TestConfigsInMain builds and runs it.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"example.com/awsv2dep"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/smithy-go/middleware"

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws"
	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws/awsconfig"
)

const (
	region = "test-region-1337"
	// userService is the service the application passes to the contrib itself.
	userService = "aws-user-service"
)

var creds = credentials.NewStaticCredentialsProvider("NOTANACCESSKEY", "NOTASECRETKEY", "")

func main() {
	ctx := context.Background()

	// A stand-in for DynamoDB that answers ListTables.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		_, _ = w.Write([]byte(`{"TableNames":[]}`))
	}))
	defer srv.Close()

	// Traced by the rules alone.
	listTables(ctx, srv.URL, "value", aws.Config{Region: region, Credentials: creds})

	ptr := &aws.Config{Region: region, Credentials: creds}
	listTables(ctx, srv.URL, "pointer", *ptr)

	newCfg := aws.NewConfig()
	newCfg.Region = region
	newCfg.Credentials = creds
	listTables(ctx, srv.URL, "new", *newCfg)

	listTables(ctx, srv.URL, "alias value", aliasedValue())
	listTables(ctx, srv.URL, "alias new", aliasedNew())

	loaded, err := config.LoadDefaultConfig(ctx, config.WithRegion(region), config.WithCredentialsProvider(creds))
	if err != nil {
		log.Fatalf("mainapp: loading config: %v", err)
	}
	listTables(ctx, srv.URL, "load", loaded)

	withAPIOptions, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(creds),
		config.WithAPIOptions(userAgentOption()),
	)
	if err != nil {
		log.Fatalf("mainapp: loading config: %v", err)
	}
	listTables(ctx, srv.URL, "load with API options", withAPIOptions)

	for style, cfg := range awsv2dep.Configs(region, creds) {
		listTables(ctx, srv.URL, "dependency "+style, cfg)
	}

	// Also traced by the application.
	user := aws.Config{Region: region, Credentials: creds}
	awstrace.AppendMiddleware(&user, awstrace.WithService(userService))
	listTables(ctx, srv.URL, "user", user)

	userLoaded, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(creds),
		awsconfig.WithDataDogTracer(awstrace.WithService(userService)),
	)
	if err != nil {
		log.Fatalf("mainapp: loading config: %v", err)
	}
	listTables(ctx, srv.URL, "user load", userLoaded)

	userWithAPIOptions, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(creds),
		config.WithAPIOptions(userAgentOption()),
		awsconfig.WithDataDogTracer(awstrace.WithService(userService)),
	)
	if err != nil {
		log.Fatalf("mainapp: loading config: %v", err)
	}
	listTables(ctx, srv.URL, "user load with API options", userWithAPIOptions)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

// userAgentOption is an API option the way an application adds its own
// middleware.
func userAgentOption() []func(*middleware.Stack) error {
	return []func(*middleware.Stack) error{awsmiddleware.AddUserAgentKeyValue("app", "mainapp")}
}

func listTables(ctx context.Context, endpoint, style string, cfg aws.Config) {
	ddb := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = &endpoint })
	if _, err := ddb.ListTables(ctx, nil); err != nil {
		log.Fatalf("mainapp: %s config: %v", style, err)
	}
}
