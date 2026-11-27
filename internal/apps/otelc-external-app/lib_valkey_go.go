// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import "github.com/valkey-io/valkey-go"

func init() {
	registerLibrary("valkey_go", func() {
		// Nothing listens on port 1, so the dial fails at once.
		if c, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{"127.0.0.1:1"}}); err == nil {
			c.Close()
		}
	})
}
