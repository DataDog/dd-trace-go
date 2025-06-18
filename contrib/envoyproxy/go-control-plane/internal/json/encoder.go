// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package json

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/waferrors"

	jsoniter "github.com/json-iterator/go"
)

type Encodable struct {
	truncated bool
	data      []byte
}

func NewEncodable(reader io.ReadCloser, limit int64) (*Encodable, error) {
	limitedReader := io.LimitedReader{
		R: reader,
		N: limit,
	}

	data, err := io.ReadAll(&limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	truncated := false
	if len(data) > int(limit) {
		data = data[:limit]
		truncated = true
	}

	return &Encodable{
		truncated: truncated,
		data:      data,
	}, nil
}

func (e *Encodable) ToEncoder(config libddwaf.EncoderConfig) *encoder {
	iter := cfg.BorrowIterator(e.data)
	return &encoder{
		Encodable: e,
		config:    config,
		iter:      iter,
	}
}

func (e *Encodable) Encode(config libddwaf.EncoderConfig, obj *libddwaf.WAFObject, depth int) (map[libddwaf.TruncationReason][]int, error) {
	encoder := e.ToEncoder(config)

	defer cfg.ReturnIterator(encoder.iter)

	err := encoder.Encode(obj, config.MaxObjectDepth-depth)
	if err != nil && (errors.Is(err, waferrors.ErrTimeout) || !e.truncated) {
		// Return an error if a waf timeout error occurred, or we are in normal parsing mode
		return nil, err
	}

	if obj.IsUnusable() {
		// Do not return an invalid root object
		return nil, fmt.Errorf("invalid json at root")
	}

	head, tail := getIteratorHeadAndTail(encoder.iter)
	if head < tail {
		// If the iterator head is less than the tail, it means that there are still bytes left in the buffer,
		// thus alerting that a structural parsing error occurred (other than due to truncation)
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}

	return encoder.truncations, nil
}

type encoder struct {
	*Encodable
	truncations map[libddwaf.TruncationReason][]int
	config      libddwaf.EncoderConfig
	iter        *jsoniter.Iterator
	iterReflect reflect.Value
}

var cfg = jsoniter.ConfigFastest

type skipError struct{}

func (skipError) Error() string {
	return "skip error"
}

var skipErr error = skipError{}

// addTruncation records a truncation event.
func (e *encoder) addTruncation(reason libddwaf.TruncationReason, size int) {
	if e.truncations == nil {
		e.truncations = make(map[libddwaf.TruncationReason][]int, 3)
	}
	e.truncations[reason] = append(e.truncations[reason], size)
}

func (e *encoder) Encode(obj *libddwaf.WAFObject, depth int) error {
	if e.config.Timer.Exhausted() {
		return waferrors.ErrTimeout
	}

	var err error

	switch e.iter.WhatIsNext() {
	case jsoniter.ObjectValue:
		return e.encodeObject(obj, depth-1)
	case jsoniter.ArrayValue:
		return e.encodeArray(obj, depth-1)
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

func (e *encoder) encodeJSONNumber(num json.Number, obj *libddwaf.WAFObject) {
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

func (e *encoder) encodeString(str string, obj *libddwaf.WAFObject) {
	strLen := len(str)
	if strLen > e.config.MaxStringSize {
		str = str[:e.config.MaxStringSize]
		e.addTruncation(libddwaf.StringTooLong, strLen)
	}

	obj.SetString(e.config.Pinner, str)
}

// encodeMapKeyFromString takes a string and a wafObject and sets the map key attribute on the wafObject to the supplied
// string. The key may be truncated if it exceeds the maximum string size allowed by the jsonEncoder.
func (e *encoder) encodeMapKeyFromString(keyStr string, obj *libddwaf.WAFObject) {
	size := len(keyStr)
	if size > e.config.MaxStringSize {
		keyStr = keyStr[:e.config.MaxStringSize]
		e.addTruncation(libddwaf.StringTooLong, size)
	}

	obj.SetMapKey(e.config.Pinner, keyStr)
}

func (e *encoder) encodeObject(parentObj *libddwaf.WAFObject, depth int) error {
	if e.config.Timer.Exhausted() {
		return waferrors.ErrTimeout
	}

	if depth < 0 {
		e.addTruncation(libddwaf.ObjectTooDeep, e.config.MaxObjectDepth-depth)
		e.iter.Skip()
		if e.iter.Error != nil {
			return e.iter.Error
		}

		return skipErr
	}

	var (
		errs    []error
		length  int
		wafObjs []libddwaf.WAFObject
	)

	e.iter.ReadObjectCB(func(_ *jsoniter.Iterator, field string) bool {
		length++
		if e.config.Timer.Exhausted() {
			errs = append(errs, waferrors.ErrTimeout)
			return false
		}

		if len(wafObjs) >= e.config.MaxContainerSize {
			e.iter.Skip()
			return true
		}

		if e.iter.Error != nil {
			// Note: We reject every object where the key field could not be parsed.
			// A valid key field is considered to be a string wrapped inside quotes followed by a colon.
			// We don't do partial parsing of the key, like assuming the key was full even if we don't detect the closing quote,
			// this could cause bad API Security schema generation.
			errs = append(errs, fmt.Errorf("failed to read object key %q: %w", field, e.iter.Error))
			return false
		}

		// The key of the object is set even if the value is invalid
		wafObjs = append(wafObjs, libddwaf.WAFObject{})
		entryObj := &wafObjs[len(wafObjs)-1]
		e.encodeMapKeyFromString(field, entryObj)

		if err := e.Encode(entryObj, depth); err != nil {
			//wafObjs = wafObjs[:len(wafObjs)-1] // Remove the last element if encoding failed
			entryObj.SetInvalid()
			if err == skipErr || errors.Is(err, io.EOF) && e.truncated {
				return true
			}

			errs = append(errs, fmt.Errorf("failed to encode value for key %q: %w", field, err))
			return false
		}

		return true
	})

	if len(wafObjs) >= e.config.MaxContainerSize {
		e.addTruncation(libddwaf.ContainerTooLarge, length)
	}

	parentObj.SetMapData(e.config.Pinner, wafObjs)
	errs = append(errs, e.iter.Error)
	return errors.Join(errs...)
}

func (e *encoder) encodeArray(parentObj *libddwaf.WAFObject, depth int) error {
	if e.config.Timer.Exhausted() {
		return waferrors.ErrTimeout
	}

	if depth < 0 {
		e.addTruncation(libddwaf.ObjectTooDeep, e.config.MaxObjectDepth-depth)
		e.iter.Skip()
		if e.iter.Error != nil {
			return e.iter.Error
		}
		return skipErr
	}

	var (
		errs    []error
		length  int
		wafObjs []libddwaf.WAFObject
	)

	e.iter.ReadArrayCB(func(_ *jsoniter.Iterator) bool {
		length++
		if e.config.Timer.Exhausted() {
			errs = append(errs, waferrors.ErrTimeout)
			return false
		}

		// We want to skip all the elements in the array if the length is reached
		if len(wafObjs) >= e.config.MaxContainerSize {
			e.iter.Skip()
			return true
		}

		wafObjs = append(wafObjs, libddwaf.WAFObject{})
		entryObj := &wafObjs[len(wafObjs)-1]

		if err := e.Encode(entryObj, depth); err != nil {
			wafObjs = wafObjs[:len(wafObjs)-1] // Remove the last element if encoding failed
			if err == skipErr {
				return true
			}

			errs = append(errs, fmt.Errorf("failed to encode array element %d: %w", len(wafObjs)-1, err))
			return false
		}

		if entryObj.IsUnusable() {
			wafObjs = wafObjs[:len(wafObjs)-1] // Remove the last element if it is nil or invalid
		}

		return true
	})

	if len(wafObjs) >= e.config.MaxContainerSize {
		e.addTruncation(libddwaf.ContainerTooLarge, length)
	}

	parentObj.SetArrayData(e.config.Pinner, wafObjs)
	errs = append(errs, e.iter.Error)
	return errors.Join(errs...)
}
