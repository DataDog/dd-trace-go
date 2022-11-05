package pproflite

import (
	"encoding/binary"
	"fmt"

	"github.com/richardartoul/molecule"
	"github.com/richardartoul/molecule/src/codec"
)

type decoder interface {
	Field
	decode(molecule.Value) error
}

func NewDecoder(input []byte) *Decoder {
	d := &Decoder{}
	d.Reset(input)
	return d
}

type Decoder struct {
	decoders []decoder
	input    []byte

	sampleType SampleType // 1
	sample     Sample     // 2
	//mapping    Mapping    // 3
	location Location // 4
	// 5
	stringTable StringTable //6
}

func (d *Decoder) Reset(input []byte) {
	d.decoders = append(d.decoders[:0],
		nil,            // field 0
		&d.sampleType,  // field 1
		nil,            //&d.sample,      // field 2
		nil,            // field 3
		&d.location,    // field 4
		nil,            // field 5
		&d.stringTable, // field 6
		nil,            // field 7
		nil,            // field 8
		nil,            // field 9
		nil,            // field 10
		nil,            // field 11
		nil,            // field 12
		nil,            // field 13
		nil,            // field 14
		// &d.mapping,
		// &d.location,
	)
	d.input = input
}

func (d *Decoder) Filter(messages ...Field) *Decoder {
	// 	for _, m := range messages {
	// 		switch m.(type) {
	// 		case SampleType:
	// 		}
	// 	}
	// 	// for _, m := range messages {

	// // }
	return d
}

func (d *Decoder) FieldEach(fn func(Field) error) error {
	return molecule.MessageEach(codec.NewBuffer(d.input), func(field int32, value molecule.Value) (bool, error) {
		if int(field) >= len(d.decoders) {
			return true, nil
		} else if decoder := d.decoders[field]; decoder == nil {
			return true, nil
		} else if err := decoder.decode(value); err != nil {
			return false, err
		} else {
			return true, fn(decoder)
		}
	})
}

func decodeFields(val molecule.Value, fields []interface{}) error {
	return molecule.MessageEach(codec.NewBuffer(val.Bytes), func(field int32, val molecule.Value) (bool, error) {
		var err error
		if int(field) >= len(fields) {
			return true, nil
		} else if field := fields[field]; field == nil {
			return true, nil
		} else {
			switch t := field.(type) {
			case *int64:
				*t = int64(val.Number)
			case *uint64:
				*t = val.Number
			case *[]int64:
				err = decodePackedInt64(val, t)
			case *[]uint64:
				err = decodePackedUint64(val, t)
			// TODO(fg) would be nice to put this logic into Sample.decode(), but
			// couldn't figure out how to do this without allocating.
			case *[]Label:
				*t = append(*t, Label{})
				err = (*t)[len(*t)-1].decode(val)
			case *[]Line:
				*t = append(*t, Line{})
				err = (*t)[len(*t)-1].decode(val)
			default:
				return false, fmt.Errorf("decodeFields: unknown type: %T", t)
			}
			return true, err
		}
	})
}

func decodePackedInt64(value molecule.Value, dst *[]int64) error {
	return decodePackedVarint(value, func(u uint64) { *dst = append(*dst, int64(u)) })
}

func decodePackedUint64(value molecule.Value, dst *[]uint64) error {
	return decodePackedVarint(value, func(u uint64) { *dst = append(*dst, u) })
}

func decodePackedVarint(value molecule.Value, f func(uint64)) error {
	switch value.WireType {
	case codec.WireVarint:
		f(value.Number)
	case codec.WireBytes:
		b := value.Bytes
		for len(b) > 0 {
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return fmt.Errorf("invalid varint")
			}
			f(v)
			b = b[n:]
		}
	default:
		return fmt.Errorf("bad wire type for DecodePackedVarint: %#v", value.WireType)
	}
	return nil
}
