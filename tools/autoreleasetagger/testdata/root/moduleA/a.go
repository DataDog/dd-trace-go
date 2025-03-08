// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package moduleA

import (
	"example.com/root"
)

var version = root.Version

// HelloA returns a greeting that includes the root module's greeting.
func HelloA() string {
	return "Hello A + " + root.HelloRoot() + " + " + version
}
