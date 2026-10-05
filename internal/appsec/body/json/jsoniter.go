// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package json

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/waferrors"

	jsoniter "github.com/json-iterator/go"
)

type jsonIterEncodable struct {
	truncated bool
	data      []byte
}

func newJSONIterEncodableFromData(data []byte, truncated bool) libddwaf.Encodable {
	// Leading and trailing whitespace carries no semantic value in JSON, so we
	// trim it in order to avoid having to worry about those when doing
	// parsing completeness assertions.
	data = bytes.TrimSpace(data)
	return &jsonIterEncodable{
		truncated: truncated,
		data:      data,
	}
}

func (e *jsonIterEncodable) Encode(enc *libddwaf.Encoder, obj *libddwaf.WAFObject, remainingDepth int) error {
	iter := cfg.BorrowIterator(e.data)
	encoder := &jsonIterEncoder{
		jsonIterEncodable: e,
		enc:               enc,
		iter:              iter,
	}

	defer cfg.ReturnIterator(encoder.iter)

	if err := encoder.Encode(obj, remainingDepth); err != nil && (errors.Is(err, waferrors.ErrTimeout) || !e.truncated) {
		// Return an error if a waf timeout error occurred, or we are in normal parsing mode
		return err
	}

	if obj.IsUnusable() {
		// Do not return an invalid root object
		return errors.New("invalid json at root")
	}

	head := getIteratorHead(encoder.iter)
	if head < len(e.data) {
		// If the iterator head is not at the end of the array, it means that there are still bytes left in the buffer,
		// thus alerting that a structural parsing error occurred (other than due to truncation)
		return errors.New("malformed JSON, expected end of input but found more data")
	}

	return nil
}

type jsonIterEncoder struct {
	*jsonIterEncodable
	enc  *libddwaf.Encoder
	iter *jsoniter.Iterator
}

var cfg = jsoniter.Config{
	MarshalFloatWith6Digits: true,
	EscapeHTML:              true,
}.Froze()

func (e *jsonIterEncoder) Encode(obj *libddwaf.WAFObject, remainingDepth int) error {
	if e.enc.Timeout() {
		return waferrors.ErrTimeout
	}

	var err error

	switch e.iter.WhatIsNext() {
	case jsoniter.ObjectValue:
		return e.encodeObject(obj, remainingDepth-1)
	case jsoniter.ArrayValue:
		return e.encodeArray(obj, remainingDepth-1)
	case jsoniter.StringValue:
		s := e.iter.ReadString()
		if err = e.iter.Error; err == nil || err == io.EOF {
			e.encodeString(s, obj)
		}
	case jsoniter.NumberValue:
		jsonNbr := e.iter.ReadNumber()
		if err = e.iter.Error; err == nil || err == io.EOF {
			err = nil
			e.encodeJSONNumber(jsonNbr, obj)
		}
	case jsoniter.BoolValue:
		b := e.iter.ReadBool()
		if err = e.iter.Error; err == nil || err == io.EOF {
			err = nil
			obj.SetBool(b)
		}
	case jsoniter.NilValue:
		e.iter.ReadNil()
		if err = e.iter.Error; err == nil || err == io.EOF {
			err = nil
			obj.SetNil()
		}
	default:
		return fmt.Errorf("unexpected JSON token: %v", e.iter.WhatIsNext())
	}

	return err
}

func (e *jsonIterEncoder) encodeJSONNumber(num json.Number, obj *libddwaf.WAFObject) {
	// Important to attempt int64 first, as this is lossless. Values that are either too small or too
	// large to be represented as int64 can be represented as float64, but this can be lossy.
	if i, err := num.Int64(); err == nil {
		obj.SetInt(i)
		return
	}

	if f, err := num.Float64(); err == nil {
		obj.SetFloat(f)
		return
	}

	// Could not store as int64 nor float, so we'll store it as a string...
	e.encodeString(num.String(), obj)
}

func (e *jsonIterEncoder) encodeString(str string, obj *libddwaf.WAFObject) {
	e.enc.WriteString(obj, str)
}

func (e *jsonIterEncoder) encodeObject(parentObj *libddwaf.WAFObject, depth int) error {
	if e.enc.Timeout() {
		return waferrors.ErrTimeout
	}

	if depth < 0 {
		e.enc.Truncations.Record(libddwaf.ObjectTooDeep, int(e.enc.Config.MaxObjectDepth)-depth)
		e.iter.Skip()
		if e.iter.Error != nil {
			return e.iter.Error
		}

		return skipErr
	}

	var (
		errs []error
		mb   = e.enc.Map(parentObj, 0)
	)
	defer mb.Close()

	e.iter.ReadObjectCB(func(_ *jsoniter.Iterator, field string) bool {
		if e.enc.Timeout() {
			errs = append(errs, waferrors.ErrTimeout)
			return false
		}

		if e.iter.Error != nil {
			// Note: We reject the object entry where the key field could not be parsed.
			// A valid key field is considered to be a string wrapped inside quotes followed by a colon.
			// We don't do partial parsing of the key, like assuming the key was full even if we don't detect the closing quote,
			// this could cause bad API Security schema generation.
			return false
		}

		// The key of the object is set even if the value is invalid
		slot := mb.NextValue(field)
		if slot == nil {
			mb.Skip()
			e.iter.Skip()
			return true
		}

		if err := e.Encode(slot, depth); err != nil {
			if errors.Is(err, io.EOF) && e.truncated {
				return false
			}

			slot.SetInvalid()
			if err == skipErr {
				return true
			}

			errs = append(errs, fmt.Errorf("failed to encode value for key %q: %w", field, err))
			return false
		}

		return true
	})

	if err := e.extractIterError(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (e *jsonIterEncoder) encodeArray(parentObj *libddwaf.WAFObject, depth int) error {
	if e.enc.Timeout() {
		return waferrors.ErrTimeout
	}

	if depth < 0 {
		e.enc.Truncations.Record(libddwaf.ObjectTooDeep, int(e.enc.Config.MaxObjectDepth)-depth)
		e.iter.Skip()
		if e.iter.Error != nil {
			return e.iter.Error
		}
		return skipErr
	}

	var (
		errs []error
		ab   = e.enc.Array(parentObj, 0)
	)
	defer ab.Close()

	e.iter.ReadArrayCB(func(_ *jsoniter.Iterator) bool {
		if e.enc.Timeout() {
			errs = append(errs, waferrors.ErrTimeout)
			return false
		}

		// We want to skip all the elements in the array if the length is reached
		slot := ab.NextValue()
		if slot == nil {
			ab.Skip()
			e.iter.Skip()
			return true
		}

		if err := e.Encode(slot, depth); err != nil {
			if errors.Is(err, io.EOF) && e.truncated {
				return false
			}

			ab.DropLast()
			if err == skipErr {
				return true
			}

			errs = append(errs, fmt.Errorf("failed to encode array element: %w", err))
			return false
		}

		if slot.IsUnusable() {
			ab.DropLast()
		}

		return true
	})

	if err := e.extractIterError(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (e *jsonIterEncoder) extractIterError() error {
	if e.iter.Error == nil {
		return nil
	}

	err := e.iter.Error
	head := getIteratorHead(e.iter)
	if head == len(e.data) {
		err = io.EOF
	}

	return err
}
