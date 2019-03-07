package tracer

import (
	"io/ioutil"
	"encoding/base64"
	"io"
	"github.com/golang/protobuf/proto"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer/binarycarrier"
)

// BinaryPropagator allows for unconstrained byte encoding of spanContext state
type BinaryPropagator struct{}


// Inject defines the Propagator to propagate SpanContext data
// out of the current process. The implementation propagates the
// TraceID and the current active SpanID, as well as the Span baggage.
func (BinaryPropagator) Inject(spanCtx ddtrace.SpanContext, opaqueCarrier interface{}) error {
	ctx, ok := spanCtx.(*spanContext)
	if !ok || ctx.traceID == 0 || ctx.spanID == 0 {
		return ErrInvalidSpanContext
	}

	data, err := proto.Marshal(&binarycarrier.BasicTracerCarrier{
		TraceId:      ctx.traceID,
		SpanId:       ctx.spanID,
		HasPriority: ctx.hasSamplingPriority(),
		SamplingPriority: int32(ctx.samplingPriority()),
		BaggageItems: ctx.baggage,
	})
	if err != nil {
		return err
	}

	switch carrier := opaqueCarrier.(type) {
	case io.Writer:
		buf := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
		base64.StdEncoding.Encode(buf, data)
		_, err = carrier.Write(buf)
		return err
	case *string:
		*carrier = base64.StdEncoding.EncodeToString(data)
	case *[]byte:
		*carrier = make([]byte, base64.StdEncoding.EncodedLen(len(data)))
		base64.StdEncoding.Encode(*carrier, data)
	default:
		return ErrInvalidCarrier
	}
	return nil
}

// Extract implements Propagator.
func (BinaryPropagator) Extract(opaqueCarrier interface{}) (ddtrace.SpanContext, error) {
	var ctx spanContext
	var data []byte
	var err error

	// Decode from string, *string, *[]byte, or []byte
	switch carrier := opaqueCarrier.(type) {
	case io.Reader:
		buf, err := ioutil.ReadAll(carrier)
		if err != nil {
			return nil, err
		}
		data, err = decodeBase64Bytes(buf)
	case *string:
		if carrier != nil {
			data, err = base64.StdEncoding.DecodeString(*carrier)
		}
	case string:
		data, err = base64.StdEncoding.DecodeString(carrier)
	case *[]byte:
		if carrier != nil {
			data, err = decodeBase64Bytes(*carrier)
		}
	case []byte:
		data, err = decodeBase64Bytes(carrier)
	default:
		return nil, ErrInvalidCarrier
	}
	if err != nil {
		return nil, err
	}

	pb := &binarycarrier.BasicTracerCarrier{}
	if err := proto.Unmarshal(data, pb); err != nil {
		return nil, err
	}
	if pb.TraceId == 0 || pb.SpanId == 0 {
		return nil, ErrSpanContextNotFound
	}
	
	ctx.traceID = pb.TraceId
	ctx.spanID =  pb.SpanId
	ctx.baggage = pb.BaggageItems
	if pb.HasPriority {
		ctx.setSamplingPriority(int(pb.SamplingPriority))
	}

	return &ctx, nil
}

func decodeBase64Bytes(in []byte) ([]byte, error) {
	data := make([]byte, base64.StdEncoding.DecodedLen(len(in)))
	n, err := base64.StdEncoding.Decode(data, in)
	if err != nil {
		return nil, err
	}
	return data[:n], nil
}