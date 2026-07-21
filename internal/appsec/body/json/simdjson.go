// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package json

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"unsafe"

	"github.com/DataDog/go-libddwaf/v5"
	"github.com/DataDog/go-libddwaf/v5/waferrors"
	json "github.com/minio/simdjson-go"
)

type Encodable struct {
	truncated  bool
	data       []byte
	parsedJSON *json.ParsedJson
}

var (
	parsedJSONPool sync.Pool
)

func NewEncodableFromData(data []byte, truncated bool) libddwaf.Encodable {
	parsedJSON, _ := parsedJSONPool.Get().(*json.ParsedJson)
	pj, err := json.Parse(data, parsedJSON, json.WithCopyStrings(false))
	if err != nil {
		// This can happen if a trivial JSON type is found like a string or number, in this case simply return a
		// simpler encoder where performance is not critical.
		return newJSONIterEncodableFromData(data, truncated)
	}

	return &Encodable{
		truncated:  truncated,
		data:       data,
		parsedJSON: pj,
	}
}

func (e *Encodable) Encode(enc *libddwaf.Encoder, obj *libddwaf.WAFObject, remainingDepth int) error {
	encoder := &encoder{
		Encodable: e,
		enc:       enc,
	}
	defer parsedJSONPool.Put(encoder.parsedJSON)

	iter := encoder.parsedJSON.Iter()
	if err := encoder.Encode(obj, iter.Advance(), &iter, remainingDepth); err != nil && (errors.Is(err, waferrors.ErrTimeout) || !e.truncated) {
		// Return an error if a waf timeout error occurred, or we are in normal parsing mode
		return err
	}

	if obj.IsUnusable() {
		// Do not return an invalid root object
		return errors.New("invalid json at root")
	}

	return nil
}

type encoder struct {
	*Encodable
	enc *libddwaf.Encoder
}

type skipError struct{}

func (skipError) Error() string {
	return "skip error"
}

var skipErr error = skipError{}

func (e *encoder) Encode(obj *libddwaf.WAFObject, typ json.Type, iter *json.Iter, remainingDepth int) (err error) {
	if e.enc.Timeout() {
		return waferrors.ErrTimeout
	}

	// EOF errors and non-fatal if we truncated the input.
	defer func() {
		if err == io.EOF && e.truncated {
			err = nil
		}

		if err != nil {
			obj.SetInvalid()
		}
	}()

	if typ == json.TypeRoot {
		typ, _, err = iter.Root(iter)
		if err != nil {
			return fmt.Errorf("failed to get root element: %w", err)
		}
	}

	switch typ {
	case json.TypeObject:
		return e.encodeObject(obj, iter, remainingDepth-1)
	case json.TypeArray:
		return e.encodeArray(obj, iter, remainingDepth-1)
	case json.TypeString:
		var value []byte
		value, err = iter.StringBytes()
		e.encodeString(value, obj)
	case json.TypeInt:
		var value int64
		value, err = iter.Int()
		obj.SetInt(value)
	case json.TypeUint:
		var value uint64
		value, err = iter.Uint()
		obj.SetUint(value)
	case json.TypeFloat:
		var value float64
		value, err = iter.Float()
		obj.SetFloat(value)
	case json.TypeBool:
		var value bool
		value, err = iter.Bool()
		obj.SetBool(value)
	case json.TypeNull:
		obj.SetNil()
	case json.TypeNone:
		err = io.EOF
	default:
		return fmt.Errorf("unexpected JSON token: %v", typ)
	}

	return err
}

func (e *encoder) encodeString(str []byte, obj *libddwaf.WAFObject) {
	e.enc.WriteString(obj, unsafe.String(unsafe.SliceData(str), len(str)))
}

func (e *encoder) encodeObject(parentObj *libddwaf.WAFObject, iter *json.Iter, depth int) error {
	if e.enc.Timeout() {
		return waferrors.ErrTimeout
	}

	if depth < 0 {
		e.enc.Truncations.Record(libddwaf.ObjectTooDeep, int(e.enc.Config.MaxObjectDepth)-depth)
		return skipErr
	}

	var (
		errs []error
		mb   = e.enc.Map(parentObj, 0)
	)
	defer mb.Close()

	var jsonObj json.Object
	_, err := iter.Object(&jsonObj)
	if err != nil {
		return err
	}

	var innerIter json.Iter
	for key, typ, err := jsonObj.NextElementBytes(&innerIter); typ != json.TypeNone; key, typ, err = jsonObj.NextElementBytes(&innerIter) {
		if e.enc.Timeout() {
			errs = append(errs, waferrors.ErrTimeout)
			break
		}

		if err != nil {
			errs = append(errs, err)
			continue
		}

		slot := mb.NextValue(unsafe.String(unsafe.SliceData(key), len(key)))
		if slot == nil {
			mb.Skip()
			innerIter.Advance()
			continue
		}

		if err := e.Encode(slot, typ, &innerIter, depth); err != nil {
			slot.SetInvalid()
			if err == skipErr || errors.Is(err, io.EOF) && e.truncated {
				continue
			}

			errs = append(errs, fmt.Errorf("failed to encode value for key %q: %w", key, err))
			break
		}
	}

	return errors.Join(errs...)
}

func (e *encoder) encodeArray(parentObj *libddwaf.WAFObject, iter *json.Iter, depth int) error {
	if e.enc.Timeout() {
		return waferrors.ErrTimeout
	}

	if depth < 0 {
		e.enc.Truncations.Record(libddwaf.ObjectTooDeep, int(e.enc.Config.MaxObjectDepth)-depth)
		return skipErr
	}

	var (
		errs []error
		ab   = e.enc.Array(parentObj, 0)
	)
	defer ab.Close()

	var jsonArray json.Array
	_, err := iter.Array(&jsonArray)
	if err != nil {
		return err
	}

	innerIter := jsonArray.Iter()
	for typ := innerIter.Advance(); typ != json.TypeNone; typ = innerIter.Advance() {
		if e.enc.Timeout() {
			errs = append(errs, waferrors.ErrTimeout)
			break
		}

		slot := ab.NextValue()
		if slot == nil {
			ab.Skip()
			continue
		}

		if err := e.Encode(slot, typ, &innerIter, depth); err != nil {
			ab.DropLast()
			if err == skipErr {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to encode value: %w", err))
			break
		}

		if slot.IsUnusable() {
			// If the entry object is unusable, we skip it and continue with the next element.
			ab.DropLast()
		}
	}

	return errors.Join(errs...)
}
