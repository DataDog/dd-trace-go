package pproflite

import (
	"fmt"
	"io"

	"github.com/richardartoul/molecule"
)

type encoder interface {
	Field
	encode(*molecule.ProtoStream) error
}

type Encoder struct {
	outWriter io.Writer
	outStream *molecule.ProtoStream
}

func (e *Encoder) Reset(w io.Writer) {
	e.outWriter = w
	if e.outStream == nil {
		e.outStream = molecule.NewProtoStream(w)
	} else {
		e.outStream.Reset(w)
	}
}

func (e *Encoder) Encode(f Field) error {
	// TODO(fg) type safety? make encode() part of Field interface?
	encoder, ok := f.(encoder)
	if !ok {
		return fmt.Errorf("field %T does not support encoder interface", f)
	}
	return e.outStream.Embedded(f.field(), encoder.encode)
}

func encodeFields(ps *molecule.ProtoStream, fields []interface{}) error {
	for i, f := range fields {
		if f == nil {
			continue
		}

		var err error
		switch t := f.(type) {
		case *int64:
			ps.Int64(i, *t)
		case *uint64:
			ps.Uint64(i, *t)
		case *[]uint64:
			if len(*t) == 1 {
				err = ps.Uint64(i, (*t)[0])
			} else {
				err = ps.Uint64Packed(i, *t)
			}
		case *[]int64:
			if len(*t) == 1 {
				err = ps.Int64(i, (*t)[0])
			} else {
				err = ps.Int64Packed(i, *t)
			}
		case *[]Label:
			for j := range *t {
				err = ps.Embedded(i, (*t)[j].encode)
			}
		default:
			err = fmt.Errorf("encodeFields: unknown type: %T", t)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
