// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than rest, which the rules must handle the way
// orchestrion does.
import (
	k8srest "k8s.io/client-go/rest"
)

func listAliased(host string) {
	list(&k8srest.Config{Host: host}, "main-alias")
}
