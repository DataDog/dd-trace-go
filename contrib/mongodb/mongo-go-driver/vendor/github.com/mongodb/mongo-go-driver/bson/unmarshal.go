package bson

import (
	"bytes"

	"github.com/mongodb/mongo-go-driver/bson/bsoncodec"
	"github.com/mongodb/mongo-go-driver/bson/bsonrw"
	"github.com/mongodb/mongo-go-driver/bson/bsontype"
)

// Unmarshaler is an interface implemented by types that can unmarshal a BSON
// document representation of themselves. The BSON bytes can be assumed to be
// valid. UnmarshalBSON must copy the BSON bytes if it wishes to retain the data
// after returning.
type Unmarshaler interface {
	UnmarshalBSON([]byte) error
}

// ValueUnmarshaler is an interface implemented by types that can unmarshal a
// BSON value representaiton of themselves. The BSON bytes and type can be
// assumed to be valid. UnmarshalBSONValue must copy the BSON value bytes if it
// wishes to retain the data after returning.
type ValueUnmarshaler interface {
	UnmarshalBSONValue(bsontype.Type, []byte) error
}

// Unmarshal parses the BSON-encoded data and stores the result in the value
// pointed to by val. If val is nil or not a pointer, Unmarshal returns
// InvalidUnmarshalError.
func Unmarshal(data []byte, val interface{}) error {
	return UnmarshalWithRegistry(DefaultRegistry, data, val)
}

// UnmarshalWithRegistry parses the BSON-encoded data using Registry r and
// stores the result in the value pointed to by val. If val is nil or not
// a pointer, UnmarshalWithRegistry returns InvalidUnmarshalError.
func UnmarshalWithRegistry(r *bsoncodec.Registry, data []byte, val interface{}) error {
	vr := bsonrw.NewBSONValueReader(data)
	return unmarshalFromReader(r, vr, val)
}

// UnmarshalExtJSON parses the extended JSON-encoded data and stores the result
// in the value pointed to by val. If val is nil or not a pointer, Unmarshal
// returns InvalidUnmarshalError.
func UnmarshalExtJSON(data []byte, canonical bool, val interface{}) error {
	return UnmarshalExtJSONWithRegistry(DefaultRegistry, data, canonical, val)
}

// UnmarshalExtJSONWithRegistry parses the extended JSON-encoded data using
// Registry r and stores the result in the value pointed to by val. If val is
// nil or not a pointer, UnmarshalWithRegistry returns InvalidUnmarshalError.
func UnmarshalExtJSONWithRegistry(r *bsoncodec.Registry, data []byte, canonical bool, val interface{}) error {
	ejvr := bsonrw.NewExtJSONValueReader(bytes.NewReader(data), canonical)
	return unmarshalFromReader(r, ejvr, val)
}

func unmarshalFromReader(r *bsoncodec.Registry, vr bsonrw.ValueReader, val interface{}) error {
	dec := decPool.Get().(*Decoder)
	defer decPool.Put(dec)

	err := dec.Reset(vr)
	if err != nil {
		return err
	}
	err = dec.SetRegistry(r)
	if err != nil {
		return err
	}

	return dec.Decode(val)
}
