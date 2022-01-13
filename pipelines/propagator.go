package pipelines

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/DataDog/sketches-go/ddsketch/encoding"
)

const (
	PropagationKey = "dd-pathway-ctx"
)

func (p Pathway) Encode() []byte {
	data := make([]byte, 8, 20)
	binary.LittleEndian.PutUint64(data, p.hash)
	encoding.EncodeVarint64(&data, p.pathwayStart.UnixNano()/int64(time.Millisecond))
	encoding.EncodeVarint64(&data, p.edgeStart.UnixNano()/int64(time.Millisecond))
	return data
}

func Decode(data []byte) (p Pathway, err error) {
	if len(data) < 8 {
		return p, errors.New("hash smaller than 8 bytes")
	}
	p.hash = binary.LittleEndian.Uint64(data)
	data = data[8:]
	pathwayStart, err := encoding.DecodeVarint64(&data)
	if err != nil {
		return p, err
	}
	edgeStart, err := encoding.DecodeVarint64(&data)
	if err != nil {
		return p, err
	}
	p.pathwayStart = time.Unix(0, pathwayStart*int64(time.Millisecond))
	p.edgeStart = time.Unix(0, edgeStart*int64(time.Millisecond))
	p.service = getService()
	return p, nil
}
