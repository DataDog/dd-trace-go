// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"context"
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"

	"github.com/DataDog/dd-trace-go/v2/internal/exportutil"
)

// partialSuccessFunc decodes a signal's OTLP 200 response body, reporting how
// many records the intake rejected (0 if none) and any accompanying message. It
// returns an error when the body is not a decodable Export*ServiceResponse, so a
// non-OTLP 200 (e.g. a proxy/login page) is surfaced as a failed export rather
// than silently counted as zero rejections.
type partialSuccessFunc func(body []byte) (rejected int64, message string, err error)

// exportEach posts each request atomically (one request -> one POST -> one
// result row), preserving input order by index. A 200 response that reports OTLP
// partial success (rejected_* > 0), or whose body does not decode as the
// expected OTLP response, is surfaced as a per-request error via partial. It
// returns a non-nil error if any request failed; per-request detail is in the
// result.
func exportEach[T proto.Message](ctx context.Context, t *rawTransport, reqs []T, partial partialSuccessFunc) (*ExportResult, error) {
	res := &ExportResult{}
	for i, r := range reqs {
		if isNilMessage(r) {
			res.Requests = append(res.Requests, RequestResult{Index: i, Err: errNilRequest})
			continue
		}
		rr, body := t.export(ctx, r)
		rr.Index = i
		if rr.Err == nil {
			switch rejected, msg, derr := partial(body); {
			case derr != nil:
				rr.Err = fmt.Errorf("otlp/export: response body is not a valid OTLP response: %w", derr)
			case rejected > 0:
				rr.Err = fmt.Errorf("otlp/export: intake reported partial success, %d record(s) rejected: %s", rejected, msg)
			}
		}
		res.Requests = append(res.Requests, rr)
	}
	return res, exportutil.Aggregate(res.Failed(), len(res.Requests), "otlp/export")
}

// isNilMessage reports whether v is a typed-nil proto message pointer, which
// would otherwise marshal to an empty body and be silently sent.
func isNilMessage[T proto.Message](v T) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Ptr && rv.IsNil()
}
