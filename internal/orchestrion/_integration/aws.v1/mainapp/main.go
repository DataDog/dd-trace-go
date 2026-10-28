// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp builds aws-sdk-go sessions in package main, where only the
// otelc rules with a target matching main apply, and calls DynamoDB with each.
// TestSessionsInMain builds and runs it.
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"example.com/awsv1dep"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2/aws"
)

// userService is the service the application passes to the contrib itself.
const userService = "aws-user-service"

var cfg = &aws.Config{
	Credentials: credentials.NewStaticCredentials("NOTANACCESSKEY", "NOTASECRETKEY", ""),
	Region:      new("test-region-1337"),
}

// Built while the package initializes.
var pkgSession = session.Must(session.NewSession(cfg))

func main() {
	// A stand-in for DynamoDB that answers ListTables.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		_, _ = w.Write([]byte(`{"TableNames":[]}`))
	}))
	defer srv.Close()

	// Traced by the rules alone.
	sess, err := session.NewSession(cfg)
	if err != nil {
		log.Fatalf("mainapp: %v", err)
	}
	listTables(srv.URL, "direct", sess)

	listTables(srv.URL, "must", session.Must(session.NewSession(cfg)))
	listTables(srv.URL, "package var", pkgSession)
	listTables(srv.URL, "alias", aliasedSession())

	depSession, err := awsv1dep.NewSession(cfg)
	if err != nil {
		log.Fatalf("mainapp: %v", err)
	}
	listTables(srv.URL, "dependency", depSession)

	withOptions, err := session.NewSessionWithOptions(session.Options{Config: *cfg})
	if err != nil {
		log.Fatalf("mainapp: %v", err)
	}
	listTables(srv.URL, "with options", withOptions)
	listTables(srv.URL, "new", session.New(cfg)) //nolint:staticcheck // session.New is deprecated but still supported

	depWithOptions, err := awsv1dep.NewSessionWithOptions(cfg)
	if err != nil {
		log.Fatalf("mainapp: %v", err)
	}
	listTables(srv.URL, "dependency with options", depWithOptions)
	listTables(srv.URL, "dependency new", awsv1dep.New(cfg))

	// Also traced by the application.
	userSession := awstrace.WrapSession(session.Must(session.NewSession(cfg)), awstrace.WithService(userService))
	listTables(srv.URL, "user", userSession)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func listTables(endpoint, style string, sess *session.Session) {
	ddb := dynamodb.New(sess, &aws.Config{Endpoint: new(endpoint)})
	if _, err := ddb.ListTables(&dynamodb.ListTablesInput{}); err != nil {
		log.Fatalf("mainapp: %s session: %v", style, err)
	}
}
