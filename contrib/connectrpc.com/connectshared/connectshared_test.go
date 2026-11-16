// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCodeString(t *testing.T) {
	for code, want := range map[Code]string{
		0:                    "code_0",
		CodeCanceled:         "canceled",
		CodeUnknown:          "unknown",
		3:                    "invalid_argument",
		CodeDeadlineExceeded: "deadline_exceeded",
		5:                    "not_found",
		6:                    "already_exists",
		7:                    "permission_denied",
		8:                    "resource_exhausted",
		9:                    "failed_precondition",
		10:                   "aborted",
		11:                   "out_of_range",
		12:                   "unimplemented",
		CodeInternal:         "internal",
		14:                   "unavailable",
		15:                   "data_loss",
		16:                   "unauthenticated",
		17:                   "code_17",
		math.MaxUint32:       "code_4294967295",
	} {
		assert.Equal(t, want, code.String(), "code %d", uint32(code))
	}
}
