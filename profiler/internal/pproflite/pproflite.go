// Package pproflite implements zero-allocation pprof encoding and decoding.
package pproflite

import (
	"github.com/richardartoul/molecule"
)

// Field holds the value of a top-level profile.proto Profile.* field.
type Field interface {
	field() int
}

// SampleType is field 1.
type SampleType struct {
	ValueType
}

func (f SampleType) field() int { return 1 }

// Sample is field 2.
type Sample struct {
	LocationID []uint64
	Value      []int64
	Label      []Label
}

func (f Sample) field() int { return 2 }

func (f *Sample) fields() []interface{} {
	return []interface{}{
		nil,
		&f.LocationID,
		&f.Value,
		&f.Label,
	}
}

func (f *Sample) decode(val molecule.Value) error {
	*f = Sample{LocationID: f.LocationID[:0], Value: f.Value[:0], Label: f.Label[:0]}
	return decodeFields(val, f.fields())
}

func (f *Sample) encode(ps *molecule.ProtoStream) error {
	return encodeFields(ps, f.fields())
}

// Label is part of Sample.
type Label struct {
	Key     int64
	Str     int64
	Num     int64
	NumUnit int64
}

func (f *Label) fields() []interface{} {
	return []interface{}{nil, &f.Key, &f.Str, &f.Num, &f.NumUnit}
}

func (f *Label) decode(val molecule.Value) error {
	*f = Label{}
	return decodeFields(val, f.fields())
}

func (f *Label) encode(ps *molecule.ProtoStream) error {
	return encodeFields(ps, f.fields())
}

// Mapping is field 3.
type Mapping struct {
	ID              uint64
	MemoryStart     uint64
	MemoryLimit     uint64
	FileOffset      uint64
	Filename        int64
	BuildID         int64
	HasFunctions    bool
	HasFilenames    bool
	HasLine_numbers bool
	HasInlineFrames bool
}

func (f Mapping) field() int { return 3 }

func (f *Mapping) fields() []interface{} {
	return []interface{}{
		nil,
		&f.ID,
		&f.MemoryStart,
		&f.MemoryLimit,
		&f.FileOffset,
		&f.Filename,
		&f.BuildID,
		&f.HasFunctions,
		&f.HasFilenames,
		&f.HasLine_numbers,
		&f.HasInlineFrames,
	}
}

func (f *Mapping) decode(val molecule.Value) error {
	*f = Mapping{}
	return decodeFields(val, f.fields())
}

func (f *Mapping) encode(ps *molecule.ProtoStream) error {
	return encodeFields(ps, f.fields())
}

// Location is field 4.
type Location struct {
	ID        uint64
	MappingID uint64
	Address   uint64
	Line      []Line
	IsFolded  bool
}

func (f Location) field() int { return 4 }

func (f *Location) fields() []interface{} {
	return []interface{}{
		nil,
		&f.ID,
		&f.MappingID,
		&f.Address,
		&f.Line,
		&f.IsFolded,
	}
}

func (f *Location) decode(val molecule.Value) error {
	*f = Location{Line: f.Line[:0]}
	return decodeFields(val, f.fields())
}

func (f *Location) encode(ps *molecule.ProtoStream) error {
	return encodeFields(ps, f.fields())
}

// Line is part of Location.
type Line struct {
	FunctionID uint64
	Line       int64
}

func (f *Line) fields() []interface{} {
	return []interface{}{nil, &f.FunctionID, &f.Line}
}

func (f *Line) decode(val molecule.Value) error {
	*f = Line{}
	return decodeFields(val, f.fields())
}

func (f *Line) encode(ps *molecule.ProtoStream) error {
	return encodeFields(ps, f.fields())
}

// Function is field 5.
type Function struct {
	ID         uint64
	Name       int64
	SystemName int64
	FileName   int64
	StartLine  int64
}

func (f Function) field() int { return 5 }

func (f *Function) fields() []interface{} {
	return []interface{}{
		nil,
		&f.ID,
		&f.Name,
		&f.SystemName,
		&f.FileName,
		&f.StartLine,
	}
}

func (f *Function) decode(val molecule.Value) error {
	*f = Function{}
	return decodeFields(val, f.fields())
}

func (f *Function) encode(ps *molecule.ProtoStream) error {
	return encodeFields(ps, f.fields())
}

// StringTable is field 6.
type StringTable struct{ Value []byte }

func (f StringTable) field() int { return 6 }

func (f *StringTable) decode(val molecule.Value) error {
	f.Value = val.Bytes
	return nil
}

func (f *StringTable) encode(ps *molecule.ProtoStream) error {
	_, err := ps.Write(f.Value)
	return err
}

// DropFrames is field 7
type DropFrames struct{ Value int64 }

func (f DropFrames) field() int { return 7 }

func (f *DropFrames) decode(val molecule.Value) error {
	f.Value = int64(val.Number)
	return nil
}

func (f *DropFrames) encodePrimitive(ps *molecule.ProtoStream) error {
	ps.Int64(f.field(), f.Value)
	return nil
}

// KeepFrames is field 8
type KeepFrames struct{ Value int64 }

func (f KeepFrames) field() int { return 8 }

func (f *KeepFrames) decode(val molecule.Value) error {
	f.Value = int64(val.Number)
	return nil
}

func (f *KeepFrames) encodePrimitive(ps *molecule.ProtoStream) error {
	ps.Int64(f.field(), f.Value)
	return nil
}

// TimeNanos is field 9
type TimeNanos struct{ Value int64 }

func (f TimeNanos) field() int { return 9 }

func (f *TimeNanos) decode(val molecule.Value) error {
	f.Value = int64(val.Number)
	return nil
}

func (f *TimeNanos) encodePrimitive(ps *molecule.ProtoStream) error {
	ps.Int64(f.field(), f.Value)
	return nil
}

// DurationNanos is field 10
type DurationNanos struct{ Value int64 }

func (f DurationNanos) field() int { return 10 }

func (f *DurationNanos) decode(val molecule.Value) error {
	f.Value = int64(val.Number)
	return nil
}

func (f *DurationNanos) encodePrimitive(ps *molecule.ProtoStream) error {
	ps.Int64(f.field(), f.Value)
	return nil
}

// PeriodType is field 11.
type PeriodType struct {
	ValueType
}

func (f PeriodType) field() int { return 11 }

// Period is field 12
type Period struct{ Value int64 }

func (f Period) field() int { return 12 }

func (f *Period) decode(val molecule.Value) error {
	f.Value = int64(val.Number)
	return nil
}

func (f *Period) encodePrimitive(ps *molecule.ProtoStream) error {
	ps.Int64(f.field(), f.Value)
	return nil
}

// Comment is field 13
type Comment struct{ Value int64 }

func (f Comment) field() int { return 13 }

func (f *Comment) decode(val molecule.Value) error {
	f.Value = int64(val.Number)
	return nil
}

func (f *Comment) encodePrimitive(ps *molecule.ProtoStream) error {
	ps.Int64(f.field(), f.Value)
	return nil
}

// DefaultSampleType is field 14
type DefaultSampleType struct{ Value int64 }

func (f DefaultSampleType) field() int { return 14 }

func (f *DefaultSampleType) decode(val molecule.Value) error {
	f.Value = int64(val.Number)
	return nil
}

func (f *DefaultSampleType) encodePrimitive(ps *molecule.ProtoStream) error {
	ps.Int64(f.field(), f.Value)
	return nil
}

// ValueType is part of SampleType and PeriodType.
type ValueType struct {
	Type int64
	Unit int64
}

func (f *ValueType) fields() []interface{} {
	return []interface{}{nil, &f.Type, &f.Unit}
}

func (f *ValueType) decode(val molecule.Value) error {
	*f = ValueType{}
	return decodeFields(val, f.fields())
}

func (f *ValueType) encode(ps *molecule.ProtoStream) error {
	return encodeFields(ps, f.fields())
}
