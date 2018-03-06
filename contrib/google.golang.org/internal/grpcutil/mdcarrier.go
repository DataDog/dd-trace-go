package grpcutil

import (
	"strings"

	"github.com/DataDog/dd-trace-go/ddtrace/tracer"

	"google.golang.org/grpc/metadata"
)

// MDCarrier implements tracer.TextMapWriter and tracer.TextMapReader on top
// of gRPC's metadata, allowing it to be used as a span context carrier for
// distributed tracing.
type MDCarrier metadata.MD

var _ tracer.TextMapWriter = (*MDCarrier)(nil)
var _ tracer.TextMapReader = (*MDCarrier)(nil)

// Get will return the first entry in the metadata at the given key.
func (mdc MDCarrier) Get(key string) string {
	if m := mdc[key]; len(m) > 0 {
		return m[0]
	}
	return ""
}

// Set will add the given value to the values found at key. Key will be lowercased to match
// the metadata implementation.
func (mdc MDCarrier) Set(key, val string) {
	k := strings.ToLower(key) // as per google.golang.org/grpc/metadata/metadata.go
	mdc[k] = append(mdc[k], val)
}

// ForeachKey will iterate over all key/value pairs in the metadata.
func (mdc MDCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, vs := range mdc {
		for _, v := range vs {
			if err := handler(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}
