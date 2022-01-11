package pipelines

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/DataDog/sketches-go/ddsketch/encoding"
)

const (
	PropagationKey = "dd-pipeline-ctx"
)

func (p Pipeline) Encode() []byte {
	data := make([]byte, 8, 14)
	binary.LittleEndian.PutUint64(data, p.hash)
	encoding.EncodeVarint64(&data, p.callTime.UnixNano()/int64(time.Millisecond))
	return data
}

func Decode(data []byte) (p Pipeline, err error) {
	if len(data) < 8 {
		return p, errors.New("pipeline hash smaller than 8 bytes")
	}
	p.hash = binary.LittleEndian.Uint64(data)
	data = data[8:]
	t, err := encoding.DecodeVarint64(&data)
	if err != nil {
		return p, err
	}
	p.callTime = time.Unix(0, t*int64(time.Millisecond))
	p.service = getService()
	return p, nil
}
