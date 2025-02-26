// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package aws provides functions to trace aws/aws-sdk-go (https://github.com/aws/aws-sdk-go).
package aws // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2/aws"
	"github.com/aws/aws-sdk-go/aws/session"
)

const (
	// SendHandlerName is the name of the Datadog NamedHandler for the Send phase of an awsv1 request
	SendHandlerName = v2.SendHandlerName
	// CompleteHandlerName is the name of the Datadog NamedHandler for the Complete phase of an awsv1 request
	CompleteHandlerName = v2.CompleteHandlerName
)

// WrapSession wraps a session.Session, causing requests and responses to be traced.
func WrapSession(s *session.Session, opts ...Option) *session.Session {
	return v2.WrapSession(s, opts...)
}
