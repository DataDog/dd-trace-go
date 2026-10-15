// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package rediswrap

import (
	"reflect"
	"unsafe"
)

// HookScanner walks reflect.Values looking for a target hook value.
// The Hook interface type, the target value, and the concrete-client skip
// predicate differ per go-redis version, so they are provided by the caller.
type HookScanner struct {
	HookType reflect.Type            // the Hook interface type
	Target   any                     // the hook value to search for
	Skip     func(reflect.Type) bool // returns true for types to skip
}

// InContainer reports whether v, or anything reachable through it, holds
// the target hook.
func (s *HookScanner) InContainer(v reflect.Value, depth int) (found, known bool) {
	if depth <= 0 {
		return false, true
	}
	// The value may be the hook itself.
	if s.matches(v) {
		return true, true
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		if v.Type().Elem() == s.HookType {
			for j := 0; j < v.Len(); j++ {
				if s.matches(v.Index(j)) {
					return true, true
				}
			}
			return false, true
		}
		// A collection of holders — []struct{ Hook }, []any — is a hook
		// store one level down: recurse into the elements.
		for j := 0; j < v.Len(); j++ {
			if found, known := s.InContainer(v.Index(j), depth-1); found || !known {
				return found, known
			}
		}
	case reflect.Map:
		if v.Type().Elem() == s.HookType {
			iter := v.MapRange()
			for iter.Next() {
				if s.matches(iter.Value()) {
					return true, true
				}
			}
		} else {
			// A map of holders is a hook store one level down.
			iter := v.MapRange()
			for iter.Next() {
				val := iter.Value()
				if val.Kind() == reflect.Struct && !val.CanAddr() {
					p := reflect.New(val.Type())
					p.Elem().Set(val)
					val = p.Elem()
				}
				if found, known := s.InContainer(val, depth-1); found || !known {
					return found, known
				}
			}
		}
		if v.Type().Key() == s.HookType {
			iter := v.MapRange()
			for iter.Next() {
				if s.matches(iter.Key()) {
					return true, true
				}
			}
		} else {
			// A generic key — map[any]struct{} — may hold the hook behind
			// its interface; walk the keys like values.
			iter := v.MapRange()
			for iter.Next() {
				hk := iter.Key()
				if hk.Kind() == reflect.Interface && !hk.IsNil() {
					hk = hk.Elem()
				}
				if hk.Kind() == reflect.Struct && !hk.CanAddr() {
					p := reflect.New(hk.Type())
					p.Elem().Set(hk)
					hk = p.Elem()
				}
				if found, known := s.InContainer(hk, depth-1); found || !known {
					return found, known
				}
			}
		}
	case reflect.Struct:
		return s.containsStruct(v, depth)
	case reflect.Interface:
		if v.IsNil() || !v.CanInterface() {
			return false, true
		}
		// The interface may itself hold the hook.
		if s.matches(v) {
			return true, true
		}
		// The indirection consumes the limit: a cycle alternating
		// interfaces and pointers cannot recurse forever.
		return s.InContainer(v.Elem(), depth-1)
	case reflect.Pointer:
		if v.IsNil() || !v.CanInterface() {
			return false, true
		}
		if s.Skip != nil && s.Skip(v.Type()) {
			return false, true
		}
		// The indirection consumes the limit.
		return s.InContainer(v.Elem(), depth-1)
	}
	return false, true
}

// InStructFields reports whether the fields of the struct s hold the target
// hook. Operates without locking: the caller serializes.
func (s *HookScanner) InStructFields(v reflect.Value, depth int) (found, known bool) {
	if v.Kind() != reflect.Struct || depth <= 0 {
		return false, true
	}
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanInterface() {
			if !f.CanAddr() {
				continue
			}
			f = reflect.NewAt(f.Type(), unsafePointerOf(f)).Elem()
		}
		if s.matches(f) {
			return true, true
		}
		switch f.Kind() {
		case reflect.Interface:
			if f.IsNil() || !f.CanInterface() {
				continue
			}
			if found, known := s.InContainer(f.Elem(), depth-1); found || !known {
				return found, known
			}
		case reflect.Slice, reflect.Array:
			if f.Type().Elem() == s.HookType {
				for j := 0; j < f.Len(); j++ {
					if s.matches(f.Index(j)) {
						return true, true
					}
				}
				continue
			}
			for j := 0; j < f.Len(); j++ {
				if found, known := s.InContainer(f.Index(j), depth-1); found || !known {
					return found, known
				}
			}
		case reflect.Map:
			if f.Type().Elem() == s.HookType {
				iter := f.MapRange()
				for iter.Next() {
					if s.matches(iter.Value()) {
						return true, true
					}
				}
			} else {
				iter := f.MapRange()
				for iter.Next() {
					val := iter.Value()
					if val.Kind() == reflect.Struct && !val.CanAddr() {
						p := reflect.New(val.Type())
						p.Elem().Set(val)
						val = p.Elem()
					}
					if found, known := s.InContainer(val, depth-1); found || !known {
						return found, known
					}
				}
			}
			if f.Type().Key() == s.HookType {
				iter := f.MapRange()
				for iter.Next() {
					if s.matches(iter.Key()) {
						return true, true
					}
				}
			} else {
				iter := f.MapRange()
				for iter.Next() {
					hk := iter.Key()
					if hk.Kind() == reflect.Interface && !hk.IsNil() {
						hk = hk.Elem()
					}
					if hk.Kind() == reflect.Struct && !hk.CanAddr() {
						p := reflect.New(hk.Type())
						p.Elem().Set(hk)
						hk = p.Elem()
					}
					if found, known := s.InContainer(hk, depth-1); found || !known {
						return found, known
					}
				}
			}
		case reflect.Struct:
			if t := f.Type(); t == mutexType || t == rwMutexType {
				continue
			}
			if found, known := s.containsStruct(f, depth-1); found || !known {
				return found, known
			}
		case reflect.Pointer:
			if f.IsNil() || !f.CanInterface() {
				continue
			}
			if s.Skip != nil && s.Skip(f.Type()) {
				continue
			}
			if found, known := s.InContainer(f.Elem(), depth-1); found || !known {
				return found, known
			}
		}
	}
	return false, true
}

// LockedInStruct reports whether the fields of the struct s hold the target
// hook, locking each nested struct as it is traversed. A lock that stays
// held leaves the result unknown.
func (s *HookScanner) LockedInStruct(v reflect.Value, depth int) (found, known bool) {
	if v.Kind() != reflect.Struct || depth <= 0 {
		return false, true
	}
	// The struct may itself be the hook.
	if s.matches(v) {
		return true, true
	}
	if v.CanAddr() {
		unlock, ok := LockStruct(v)
		defer unlock()
		if !ok {
			return false, false
		}
	}
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanInterface() {
			if !f.CanAddr() {
				continue
			}
			f = reflect.NewAt(f.Type(), unsafePointerOf(f)).Elem()
		}
		if s.matches(f) {
			return true, true
		}
		switch f.Kind() {
		case reflect.Interface:
			if f.IsNil() || !f.CanInterface() {
				continue
			}
			if found, known := s.InContainer(f.Elem(), depth-1); found || !known {
				return found, known
			}
		case reflect.Slice, reflect.Array:
			if f.Type().Elem() == s.HookType {
				for j := 0; j < f.Len(); j++ {
					if s.matches(f.Index(j)) {
						return true, true
					}
				}
				continue
			}
			for j := 0; j < f.Len(); j++ {
				if found, known := s.InContainer(f.Index(j), depth-1); found || !known {
					return found, known
				}
			}
		case reflect.Map:
			if f.Type().Elem() == s.HookType {
				iter := f.MapRange()
				for iter.Next() {
					if s.matches(iter.Value()) {
						return true, true
					}
				}
			} else {
				iter := f.MapRange()
				for iter.Next() {
					val := iter.Value()
					if val.Kind() == reflect.Struct && !val.CanAddr() {
						p := reflect.New(val.Type())
						p.Elem().Set(val)
						val = p.Elem()
					}
					if found, known := s.InContainer(val, depth-1); found || !known {
						return found, known
					}
				}
			}
			if f.Type().Key() == s.HookType {
				iter := f.MapRange()
				for iter.Next() {
					if s.matches(iter.Key()) {
						return true, true
					}
				}
			} else {
				iter := f.MapRange()
				for iter.Next() {
					hk := iter.Key()
					if hk.Kind() == reflect.Interface && !hk.IsNil() {
						hk = hk.Elem()
					}
					if hk.Kind() == reflect.Struct && !hk.CanAddr() {
						p := reflect.New(hk.Type())
						p.Elem().Set(hk)
						hk = p.Elem()
					}
					if found, known := s.InContainer(hk, depth-1); found || !known {
						return found, known
					}
				}
			}
		case reflect.Struct:
			if t := f.Type(); t == mutexType || t == rwMutexType {
				continue
			}
			if found, known := s.LockedInStruct(f, depth-1); found || !known {
				return found, known
			}
		case reflect.Pointer:
			if f.IsNil() || !f.CanInterface() {
				continue
			}
			if s.Skip != nil && s.Skip(f.Type()) {
				continue
			}
			if found, known := s.InContainer(f.Elem(), depth-1); found || !known {
				return found, known
			}
		}
	}
	return false, true
}

// containsStruct scans a struct field that is a nested struct, skipping
// mutex-typed fields.
func (s *HookScanner) containsStruct(v reflect.Value, depth int) (found, known bool) {
	if t := v.Type(); t == mutexType || t == rwMutexType {
		return false, true
	}
	return s.LockedInStruct(v, depth)
}

// matches reports whether v holds the target hook value.
func (s *HookScanner) matches(v reflect.Value) bool {
	if !v.CanInterface() {
		return false
	}
	return safeEqual(v.Interface(), s.Target)
}

// unsafePointerOf returns the address of an addressable reflect.Value.
func unsafePointerOf(v reflect.Value) unsafe.Pointer {
	return unsafe.Pointer(v.UnsafeAddr())
}
