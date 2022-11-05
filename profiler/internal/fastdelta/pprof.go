package fastdelta

import (
	"encoding/binary"
	"fmt"

	"github.com/richardartoul/molecule"
	"github.com/richardartoul/molecule/src/codec"
)

type Decoder interface {
	Decode(molecule.Value) error
}

func NewPPROFDecoder() *PPROFDecoder {
	return &PPROFDecoder{decoders: make([]Decoder, recProfileDefaultSampleType+1)}
}

type PPROFDecoder struct {
	decoders []Decoder
}

func (p *PPROFDecoder) Decode(buf []byte, messages []Decoder, fn func(m Decoder) error) error {
	for i := range p.decoders {
		p.decoders[i] = nil
	}
	for _, m := range messages {
		switch m.(type) {
		case *StringTable:
			p.decoders[recProfileStringTable] = m
		case *ValueType:
			p.decoders[recValueTypeType] = m
		case *Sample:
			p.decoders[recProfileSample] = m
		case *Location:
			p.decoders[recProfileLocation] = m
		}
	}

	return molecule.MessageEach(codec.NewBuffer(buf), func(field int32, value molecule.Value) (bool, error) {
		decoder := p.decoders[field]
		if decoder == nil {
			return true, nil
		} else if err := decoder.Decode(value); err != nil {
			return false, err
		}
		return true, fn(decoder)
	})
}

type ValueType struct {
	Type int64
	Unit int64
}

func (v *ValueType) reset() {
	*v = ValueType{}
}

func (v *ValueType) Decode(val molecule.Value) error {
	return molecule.MessageEach(codec.NewBuffer(val.Bytes), func(field int32, value molecule.Value) (bool, error) {
		switch ValueTypeRecordNumber(field) {
		case recValueTypeType:
			v.Type = int64(value.Number)
		case recValueTypeUnit:
			v.Unit = int64(value.Number)
		}
		return true, nil
	})
}

type Location struct {
	ID        uint64
	MappingID uint64
	Address   uint64
	Line      []Line
	IsFolded  bool
}

func (l *Location) reset() {
	*l = Location{Line: l.Line[:0]}
}

func (l *Location) Decode(val molecule.Value) error {
	l.reset()
	return molecule.MessageEach(codec.NewBuffer(val.Bytes), func(field int32, value molecule.Value) (bool, error) {
		switch LocationRecordNumber(field) {
		case recLocationID:
			l.ID = value.Number
		case recLocationMappingID:
			l.MappingID = value.Number
		case recLocationAddress:
			l.Address = value.Number
		case recLocationLine:
			l.Line = append(l.Line, Line{})
			l.Line[len(l.Line)-1].Decode(codec.NewBuffer(value.Bytes))
			// TODO: parse IsFolded?
		}
		return true, nil
	})
}

type Line struct {
	FunctionID uint64
	Line       int64
}

func (l *Line) Decode(buf *codec.Buffer) error {
	return molecule.MessageEach(buf, func(field int32, value molecule.Value) (bool, error) {
		switch LineRecordNumber(field) {
		case recLineFunctionID:
			l.FunctionID = value.Number
			return false, nil
			// TODO: parse Line?
		}
		return true, nil
	})
}

type Sample struct {
	LocationID []uint64
	Value      []int64
	Label      []Label
}

func (s *Sample) reset() {
	*s = Sample{
		LocationID: s.LocationID[:0],
		Value:      s.Value[:0],
		Label:      s.Label[:0],
	}
}

func (s *Sample) Encode(ps *molecule.ProtoStream) error {
	var fe firstErr
	if len(s.LocationID) == 1 {
		// Produces slightly more compact output, but we mostly do this so that
		// TestCompaction passes.
		fe.Add(ps.Uint64(int(recSampleLocationID), s.LocationID[0]))
	} else {
		fe.Add(ps.Uint64Packed(int(recSampleLocationID), s.LocationID))
	}
	fe.Add(ps.Int64Packed(int(recSampleValue), s.Value))
	for _, l := range s.Label {
		fe.Add(ps.Embedded(int(recSampleLabel), func(ps *molecule.ProtoStream) error {
			return l.Encode(ps)
		}))
	}
	return fe.Err()
}

func (s *Sample) Decode(val molecule.Value) error {
	s.reset()
	return molecule.MessageEach(codec.NewBuffer(val.Bytes), func(field int32, value molecule.Value) (bool, error) {
		switch SampleRecordNumber(field) {
		case recSampleLocationID:
			return true, decodePackedUint64(value, &s.LocationID)
		case recSampleValue:
			return true, decodePackedInt64(value, &s.Value)
		case recSampleLabel:
			s.Label = append(s.Label, Label{})
			s.Label[len(s.Label)-1].Decode(codec.NewBuffer(value.Bytes))
		}
		return true, nil
	})
}

func (s *Sample) ValidStrings(st *stringTable) error {
	for _, l := range s.Label {
		if err := l.ValidStrings(st); err != nil {
			return err
		}
	}
	return nil
}

type Label struct {
	Key     int64
	Str     int64
	Num     int64
	NumUnit int64
}

func (l *Label) Encode(ps *molecule.ProtoStream) error {
	var fe firstErr
	fe.Add(ps.Int64(int(recLabelKey), l.Key))
	fe.Add(ps.Int64(int(recLabelStr), l.Str))
	fe.Add(ps.Int64(int(recLabelNum), l.Num))
	fe.Add(ps.Int64(int(recLabelNumUnit), l.NumUnit))
	return fe.Err()
}

func (l *Label) Decode(buf *codec.Buffer) error {
	return molecule.MessageEach(buf, func(field int32, value molecule.Value) (bool, error) {
		switch LabelRecordNumber(field) {
		case recLabelKey:
			l.Key = int64(value.Number)
		case recLabelStr:
			l.Str = int64(value.Number)
		case recLabelNum:
			l.Num = int64(value.Number)
		case recLabelNumUnit:
			l.NumUnit = int64(value.Number)
		}
		return true, nil
	})
}

func (l *Label) ValidStrings(st *stringTable) error {
	if !st.contains(uint64(l.Key)) {
		return fmt.Errorf("invalid string index %d", l.Key)
	}
	if !st.contains(uint64(l.Str)) {
		return fmt.Errorf("invalid string index %d", l.Str)
	}
	if !st.contains(uint64(l.NumUnit)) {
		return fmt.Errorf("invalid string index %d", l.NumUnit)
	}
	return nil
}

type StringTable struct {
	Bytes []byte
}

func (l *StringTable) Decode(val molecule.Value) error {
	l.Bytes = val.Bytes
	return nil
}

type firstErr struct {
	err error
}

func (f *firstErr) Add(err error) {
	if err != nil && f.err == nil {
		f.err = err
	}
}

func (f *firstErr) Err() error {
	return f.err
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
