// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import "github.com/twmb/franz-go/pkg/kgo"

func init() {
	registerLibrary("franz_go", func() {
		// The client connects only when it first sends a request.
		if cl, err := kgo.NewClient(); err == nil {
			cl.Close()
		}
	})
}
