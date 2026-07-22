// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal"
)

// SetMetaStructTag sets a tag under `meta_struct` if that field is supported by
// the agent, and otherwise sets a fallback tag and value. This enables more
// efficient processing for messagepack-encodable values where supported.
func SetMetaStructTag(span *tracer.Span,
	metaStructTag string,
	metaStructValue msgp.Marshaler,
	fallbackTag string,
	fallbackValue any,
) {
	if tracer.MetaStructAvailable() {
		span.SetTag(metaStructTag, internal.MetaStructValue{Value: metaStructValue})
	} else {
		span.SetTag(fallbackTag, fallbackValue)
	}
}
