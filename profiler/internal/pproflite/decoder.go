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

	sampleType        SampleType        // 1
	sample            Sample            // 2
	mapping           Mapping           // 3
	location          Location          // 4
	locationID        LocationID        // 4 ID only optimization
	function          Function          // 5
	stringTable       StringTable       // 6
	dropFrames        DropFrames        // 7
	keepFrames        KeepFrames        // 8
	timeNanos         TimeNanos         // 9
	durationNanos     DurationNanos     // 10
	periodType        PeriodType        // 11
	period            Period            // 12
	comment           Comment           // 13
	defaultSampleType DefaultSampleType // 14
}

func (d *Decoder) Reset(input []byte) {
	d.filter()
	d.input = input
}

func (d *Decoder) filter(fields ...Field) *Decoder {
	d.decoders = append(d.decoders[:0],
		nil,                  // field 0
		&d.sampleType,        // field 1
		&d.sample,            // field 2
		&d.mapping,           // field 3
		&d.location,          // field 4
		&d.function,          // field 5
		&d.stringTable,       // field 6
		&d.dropFrames,        // field 7
		&d.keepFrames,        // field 8
		&d.timeNanos,         // field 9
		&d.durationNanos,     // field 10
		&d.periodType,        // field 11
		&d.period,            // field 12
		&d.comment,           // field 13
		&d.defaultSampleType, // field 14
	)

	if len(fields) > 0 {
		for i := range d.decoders {
			include := false
			for _, f := range fields {
				if f.field() == i {
					include = true
					switch f.(type) {
					case LocationID:
						d.decoders[i] = &d.locationID
					}
					break
				}
			}
			if !include {
				d.decoders[i] = nil
			}
		}

	}

	return d
}

func (d *Decoder) FieldEachFilter(fn func(Field) error, filter ...Field) error {
	defer d.filter() // reset
	d.filter(filter...)
	return d.FieldEach(fn)
}

// FieldEach invokes fn for every decoded Field and resets any applied Filter.
func (d *Decoder) FieldEach(fn func(Field) error) error {
	return molecule.MessageEach(codec.NewBuffer(d.input), func(field int32, value molecule.Value) (bool, error) {
		if int(field) >= len(d.decoders) {
			return true, nil
		} else if decoder := d.decoders[field]; decoder == nil {
			return true, nil
		} else if err := decoder.decode(value); err != nil {
			return false, err
		} else if err := fn(decoder); err != nil {
			return false, err
		} else {
			return true, nil
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
			case *bool:
				*t = val.Number == 1
			// NOTE: *[]Label and *[]Line used to be handled here before hand-rolling
			// the decoding of their parent messages.
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
