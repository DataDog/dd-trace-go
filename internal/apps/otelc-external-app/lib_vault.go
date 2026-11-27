// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import vaultapi "github.com/hashicorp/vault/api"

func init() {
	registerLibrary("vault", func() {
		_ = &vaultapi.Config{Address: "http://127.0.0.1"}
	})
}
