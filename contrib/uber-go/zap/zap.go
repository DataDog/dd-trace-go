package zap

import (
	"context"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageUberGoZap)
}

type config struct {
	log128bits         bool
	correlationEnabled bool
}

var cfg = newConfig()

func newConfig() *config {
	return &config{
		log128bits:         options.GetBoolEnv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", true),
		correlationEnabled: options.GetBoolEnv("DD_TRACE_ZAP_CORRELATION_INJECTION_ENABLED", true),
	}
}

func ExtractTracingContext(ctx context.Context) zap.Field {
	if !cfg.correlationEnabled {
		return zap.Skip()
	}

	span, found := tracer.SpanFromContext(ctx)
	if !found {
		return zap.Skip()
	}

	return zap.Inline(zapcore.ObjectMarshalerFunc(func(enc zapcore.ObjectEncoder) error {
		if cfg.log128bits && span.Context().TraceID() != tracer.TraceIDZero {
			enc.AddString(ext.LogKeyTraceID, span.Context().TraceID())
		} else {
			enc.AddString(ext.LogKeyTraceID, strconv.FormatUint(span.Context().TraceIDLower(), 10))
		}
		enc.AddUint64(ext.LogKeySpanID, span.Context().SpanID())
		return nil
	}))
}
