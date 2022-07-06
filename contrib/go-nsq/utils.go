package nsq

import (
	"bytes"
	"context"
	"encoding/gob"
	"math"
	"path"
	"reflect"
	"runtime"
	"sync"

	"github.com/nsqio/go-nsq"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

var bfp = sync.Pool{
	New: func() interface{} { return bytes.NewBuffer(nil) },
}

func getBuf() *bytes.Buffer {
	buf := bfp.Get().(*bytes.Buffer)
	buf.Reset()

	return buf
}

func putBuf(buf *bytes.Buffer) {
	bfp.Put(buf)
}

type __sep__ struct{}

var sep []byte

// inject will inject span context into raw message body, after injection the
// new message body looks like below.
// sep|origin body|sep|tracing carrier
func inject(span tracer.Span, body []byte) ([]byte, error) {
	if hasSpanContext(body) || span == nil || span.Context() == nil || span.Context().TraceID() <= 0 || span.Context().SpanID() <= 0 {
		return body, nil
	}

	carri := make(tracer.TextMapCarrier)
	err := tracer.Inject(span.Context(), carri)
	if err != nil {
		return nil, err
	}

	buf := getBuf()
	defer putBuf(buf)

	if err = gob.NewEncoder(buf).Encode(carri); err != nil {
		return nil, err
	}

	bts := make([]byte, len(sep)+len(body)+len(sep)+buf.Len())
	i := copy(bts, sep)
	i += copy(bts[i:], body)
	i += copy(bts[i:], sep)
	copy(bts[i:], buf.Bytes())

	return bts, nil
}

// extrace will extract span context from message body.
func extract(body []byte) (ddtrace.SpanContext, []byte, error) {
	if !hasSpanContext(body) {
		return nil, body, nil
	}

	comb := bytes.Split(body[len(sep):], sep)
	carri := make(tracer.TextMapCarrier)
	if err := gob.NewDecoder(bytes.NewBuffer(comb[1])).Decode(&carri); err != nil {
		return nil, nil, err
	}

	spnctx, err := tracer.Extract(carri)

	return spnctx, comb[0], err
}

func hasSpanContext(body []byte) bool {
	for i, b := range sep {
		if body[i] != b {
			return false
		}
	}

	return bytes.Count(body[len(sep):], sep) == 1
}

func bodySize(body [][]byte) int {
	var size int
	for i := range body {
		size += len(body[i])
	}

	return size
}

// startSpanFromContext will try to start span from a given context.
func startSpanFromContext(ctx context.Context, config *clientConfig, nsqconfig *nsq.Config, resource, funcName string) (tracer.Span, context.Context) {
	if config == nil {
		cfg := &clientConfig{}
		defaultConfig(cfg)
		config = cfg
	}

	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.ServiceName(config.serviceName),
		tracer.ResourceName(resource),
	}
	if !math.IsNaN(config.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, config.analyticsRate))
	}
	if nsqconfig != nil {
		opts = append(opts, []tracer.StartSpanOption{
			tracer.Tag(LocalAddr, nsqconfig.LocalAddr),
			tracer.Tag(ClientID, nsqconfig.ClientID),
			tracer.Tag(Hostname, nsqconfig.Hostname),
			tracer.Tag(UserAgent, nsqconfig.UserAgent),
			tracer.Tag(SampleRate, nsqconfig.SampleRate),
			tracer.Tag(Deflate, nsqconfig.Deflate),
			tracer.Tag(DeflateLevel, nsqconfig.DeflateLevel),
			tracer.Tag(Snappy, nsqconfig.Snappy),
		}...)
	}

	return tracer.StartSpanFromContext(ctx, funcName, opts...)
}

func getFuncName(f interface{}) string {
	return path.Base(runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name())
}

func init() {
	buf := bytes.NewBuffer(nil)
	gob.NewEncoder(buf).Encode(__sep__{})
	sep = buf.Bytes()
}
