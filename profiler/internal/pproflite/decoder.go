package pproflite

import (
	"encoding/binary"
	"fmt"

	"github.com/richardartoul/molecule"
	"github.com/richardartoul/molecule/src/codec"
)

type FieldDecoder int

// Important: For fields with multiple decoders, list the default
// decoder first here (e.g. Location before LocationID).
const (
	SampleTypeDecoder FieldDecoder = iota
	SampleDecoder
	MappingDecoder
	LocationDecoder
	LocationIDDecoder
	FunctionDecoder
	StringTableDecoder
	DropFramesDecoder
	KeepFramesDecoder
	TimeNanosDecoder
	DurationNanosDecoder
	PeriodTypeDecoder
	PeriodDecoder
	CommentDecoder
	DefaultSampleTypeDecoder
	sampleTypeLast
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
	d.input = input
}

// FieldEachOld invokes fn for every decoded Field. If filters are provided,
// only fields matching the filters will be decoded.
func (d *Decoder) FieldEach(fn func(Field) error, filter ...FieldDecoder) error {
	if err := d.applyFilter(filter...); err != nil {
		return err
	}
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

func (d *Decoder) applyFilter(fields ...FieldDecoder) error {
	lookupDecoder := func(fd FieldDecoder) (decoder, error) {
		switch fd {
		case SampleTypeDecoder:
			return &d.sampleType, nil
		case SampleDecoder:
			return &d.sample, nil
		case MappingDecoder:
			return &d.mapping, nil
		case LocationDecoder:
			return &d.location, nil
		case LocationIDDecoder:
			return &d.locationID, nil
		case FunctionDecoder:
			return &d.function, nil
		case StringTableDecoder:
			return &d.stringTable, nil
		case DropFramesDecoder:
			return &d.dropFrames, nil
		case KeepFramesDecoder:
			return &d.keepFrames, nil
		case TimeNanosDecoder:
			return &d.timeNanos, nil
		case DurationNanosDecoder:
			return &d.durationNanos, nil
		case PeriodTypeDecoder:
			return &d.periodType, nil
		case PeriodDecoder:
			return &d.period, nil
		case CommentDecoder:
			return &d.comment, nil
		case DefaultSampleTypeDecoder:
			return &d.defaultSampleType, nil
		}
		return nil, fmt.Errorf("applyFilter: unknown filter: %#v", fd)
	}

	d.decoders = d.decoders[:0]

	if len(fields) == 0 {
		// Reverse order to default to Location instead of LocationID decoder.
		for fd := sampleTypeLast - 1; fd >= 0; fd-- {
			decoder, err := lookupDecoder(fd)
			if err != nil {
				return err
			}
			for len(d.decoders) <= decoder.field() {
				d.decoders = append(d.decoders, nil)
			}
			d.decoders[decoder.field()] = decoder
		}
	}

	for _, fd := range fields {
		decoder, err := lookupDecoder(fd)
		if err != nil {
			return err
		}
		for len(d.decoders) <= decoder.field() {
			d.decoders = append(d.decoders, nil)
		}
		d.decoders[decoder.field()] = decoder
	}
	return nil
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
				// note: might be worth optimizing this and the function below
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
