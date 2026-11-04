// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than session, which the rule must handle the
// way orchestrion does.
import (
	awssession "github.com/aws/aws-sdk-go/aws/session"
)

func aliasedSession() *awssession.Session {
	return awssession.Must(awssession.NewSession(cfg))
}
