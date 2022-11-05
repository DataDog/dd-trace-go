package pproflite

import (
	"github.com/richardartoul/molecule"
)

// Field holds the value of a top-level profile.proto Profile.* field.
type Field interface {
	field()
}

// SampleType is field 1.
type SampleType struct {
	ValueType
}

// ValueType is part of SampleType and PeriodType.
type ValueType struct {
	Type int64
	Unit int64
}

func (f ValueType) field() {}

func (f *ValueType) decode(val molecule.Value) error {
	*f = ValueType{}
	return decodeFields(val, []interface{}{nil, &f.Type, &f.Unit})
}

// Sample is field 2.
type Sample struct {
	LocationID []uint64
	Value      []int64
	Label      []Label
}

func (f Sample) field() {}

func (f *Sample) decode(val molecule.Value) error {
	*f = Sample{LocationID: f.LocationID[:0], Value: f.Value[:0], Label: f.Label[:0]}
	return decodeFields(val, []interface{}{
		nil,
		&f.LocationID,
		&f.Value,
		&f.Label,
	})
}

// Label is part of Sample.
type Label struct {
	Key     int64
	Str     int64
	Num     int64
	NumUnit int64
}

func (f *Label) decode(val molecule.Value) error {
	*f = Label{}
	return decodeFields(val, []interface{}{nil, &f.Key, &f.Str, &f.Num, &f.NumUnit})
}

// Mapping is field 3.
type Mapping struct{}

// Location is field 4.
type Location struct {
	ID        uint64
	MappingID uint64
	Address   uint64
	Line      []Line
	IsFolded  bool
}

func (f Location) field() {}

func (f *Location) decode(val molecule.Value) error {
	*f = Location{Line: f.Line[:0]}
	return decodeFields(val, []interface{}{
		nil,
		&f.ID,
		&f.MappingID,
		&f.Address,
		&f.Line,
		// &f.IsFolded, TODO
	})
}

// Line is part of Location.
type Line struct {
	FunctionID uint64
	Line       int64
}

func (f *Line) decode(val molecule.Value) error {
	*f = Line{}
	return decodeFields(val, []interface{}{nil, &f.FunctionID, &f.Line})
}

// StringTable is field 6.
type StringTable struct {
	Value []byte
}

func (f StringTable) field() {}

func (f *StringTable) decode(val molecule.Value) error {
	f.Value = val.Bytes
	return nil
}

// PeriodType is field 11.
type PeriodType struct {
	ValueType
}
