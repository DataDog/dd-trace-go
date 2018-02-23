package tracer

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/ugorji/go/codec"
)

// encoding specifies a supported encoding type that can be used by encode.
type encoding int

const (
	// encodingMsgpack is "msgpack" encoding.
	encodingMsgpack encoding = iota

	// encodingJSON is JSON encoding.
	encodingJSON
)

// contentType returns the HTTP content type for the encoding.
func (e encoding) contentType() string {
	switch e {
	case encodingMsgpack:
		return "application/msgpack"
	case encodingJSON:
		return "application/json"
	}
	return ""
}

var mh codec.MsgpackHandle

// encode encodes v using the given encoding and returns an io.Reader which reads
// the encoded data.
func encode(e encoding, v interface{}) (io.Reader, error) {
	var (
		buf bytes.Buffer
		err error
	)
	// It might be tempting to use an io.Pipe here instead of the buffer in an attempt
	// to save memory. While some memory *is* saved, it comes at a great cost:
	//
	// name                     old time/op    new time/op     delta
	// MsgpackEncoder/small-8      130µs ± 0%     3654µs ± 0%   ~     (p=1.000 n=1+1)
	// MsgpackEncoder/medium-8    3.14ms ± 0%    89.58ms ± 0%   ~     (p=1.000 n=1+1)
	// MsgpackEncoder/large-8      130ms ± 0%     3686ms ± 0%   ~     (p=1.000 n=1+1)
	//
	// name                     old speed      new speed       delta
	// MsgpackEncoder/small-8    153MB/s ± 0%      5MB/s ± 0%   ~     (p=1.000 n=1+1)
	// MsgpackEncoder/medium-8   158MB/s ± 0%      6MB/s ± 0%   ~     (p=1.000 n=1+1)
	// MsgpackEncoder/large-8    153MB/s ± 0%      5MB/s ± 0%   ~     (p=1.000 n=1+1)
	//
	// name                     old alloc/op   new alloc/op    delta
	// MsgpackEncoder/small-8      146kB ± 0%       87kB ± 0%   ~     (p=1.000 n=1+1)
	// MsgpackEncoder/medium-8    2.21MB ± 0%     1.57MB ± 0%   ~     (p=1.000 n=1+1)
	// MsgpackEncoder/large-8      141MB ± 0%       88MB ± 0%   ~     (p=1.000 n=1+1)
	//
	// name                     old allocs/op  new allocs/op   delta
	// MsgpackEncoder/small-8       21.0 ± 0%     1918.0 ± 0%   ~     (p=1.000 n=1+1)
	// MsgpackEncoder/medium-8      30.0 ± 0%    47525.0 ± 0%   ~     (p=1.000 n=1+1)
	// MsgpackEncoder/large-8       43.0 ± 0%  1900047.0 ± 0%   ~     (p=1.000 n=1+1)
	//
	// Profiling will reveal that the (significant) loss in speed is due to the
	// synchronization that the io.Pipe implementation uses internally.
	switch e {
	case encodingJSON:
		err = json.NewEncoder(&buf).Encode(v)
	case encodingMsgpack:
		err = codec.NewEncoder(&buf, &mh).Encode(v)
	default:
		panic("unsupported encoding")
	}
	return &buf, err
}
