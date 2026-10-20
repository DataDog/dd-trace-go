// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import "example.com/brokendep"

// Reads itself type-checks cleanly: go/packages marks it IllTyped with no
// Errors of its own, because the type error lives in the imported
// dependency, not here.
func Reads() string {
	return brokendep.Broken()
}
