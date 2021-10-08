// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !without_ddwaf && cgo && !windows && amd64 && (linux || darwin)
// +build !without_ddwaf
// +build cgo
// +build !windows
// +build amd64
// +build linux darwin

package bindings

// #include <stdlib.h>
// #include <string.h>
// #include "ddwaf.h"
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// Version wrapper type of the WAF version.
type Version C.ddwaf_version

// String returns the string representation of the version in the form <major>.<minor>.<patch>.
func (v *Version) String() string {
	major := uint16(v.major)
	minor := uint16(v.minor)
	patch := uint16(v.patch)
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

// Health allows knowing if the WAF can be used. It returns the current WAF version and a nil error when the WAF
// library is healthy. Otherwise, it returns a nil version and an error describing the issue.
func Health() (Version, error) {
	var v C.ddwaf_version
	C.ddwaf_get_version(&v)
	return Version(v), nil
}

// WAF represents an instance of the WAF for a given ruleset.
type WAF struct {
	handle C.ddwaf_handle
	// RWMutex used as a simple reference counter implementation allowing to safely release the WAF handle only
	// when there are no WAFContext using it.
	mu sync.RWMutex

	encoder encoder
}

func NewWAF(jsonRule []byte) (*WAF, error) {
	var rule interface{}
	if err := json.Unmarshal(jsonRule, &rule); err != nil {
		return nil, fmt.Errorf("could not parse the WAF rule: %v", err)
	}

	encoder := encoder{
		maxDepth:        C.DDWAF_MAX_MAP_DEPTH,
		maxStringLength: C.DDWAF_MAX_STRING_LENGTH,
		maxArrayLength:  C.DDWAF_MAX_ARRAY_LENGTH,
		maxMapLength:    C.DDWAF_MAX_ARRAY_LENGTH,
	}

	wafRule, err := encoder.encode(rule)
	if err != nil {
		return nil, fmt.Errorf("could not encode the JSON WAF rule into a WAF object: %v", err)
	}
	defer free(wafRule)

	handle := C.ddwaf_init(wafRule.ctype(), &C.ddwaf_config{
		maxArrayLength: C.uint64_t(encoder.maxArrayLength),
		maxMapDepth:    C.uint64_t(encoder.maxMapLength),
	})
	if handle == nil {
		return nil, errors.New("could not instantiate the waf rule")
	}
	incNbLiveCObjects()

	return &WAF{
		handle:  handle,
		encoder: encoder,
	}, nil
}

// Close the WAF rule. The underlying C memory is released as soon as there are
// no more execution contexts using the rule.
func (waf *WAF) Close() error {
	// Exclusively lock the mutex to ensure there's no other concurrent WAFContext using the WAF handle.
	waf.mu.Lock()
	defer waf.mu.Unlock()
	C.ddwaf_destroy(waf.handle)
	decNbLiveCObjects()
	waf.handle = nil
	return nil
}

type WAFContext struct {
	waf *WAF

	context C.ddwaf_context
	// Mutex protecting the use of context which is not thread-safe.
	mu sync.Mutex
}

func NewWAFContext(waf *WAF) *WAFContext {
	// Increase the WAF handle usage count by RLock'ing the mutex.
	// It will be RUnlock'ed in the Close method when the WAFContext is released.
	waf.mu.RLock()
	if waf.handle == nil {
		// The WAF handle got free'd by the time we got the lock
		waf.mu.RUnlock()
		return nil
	}
	context := C.ddwaf_context_init(waf.handle, nil)
	if context == nil {
		return nil
	}
	incNbLiveCObjects()
	return &WAFContext{
		waf:     waf,
		context: context,
	}
}

func (c *WAFContext) Run(values map[string]interface{}, timeout time.Duration) (action Action, md []byte, err error) {
	if len(values) == 0 {
		return
	}
	wafValue, err := c.waf.encoder.encode(values)
	if err != nil {
		return 0, nil, err
	}
	defer free(wafValue)
	return c.run(wafValue, timeout)
}

func (c *WAFContext) run(data *wafObject, timeout time.Duration) (action Action, md []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result C.ddwaf_result
	defer C.ddwaf_result_free(&result)
	C.ddwaf_run(c.context, data.ctype(), &result, C.size_t(timeout/time.Microsecond))
	return goReturnValues(&result)

}

func (c *WAFContext) Close() error {
	// RUnlock the WAF RWMutex to decrease the count of WAFContexts using it.
	defer c.waf.mu.RUnlock()
	C.ddwaf_context_destroy(c.context)
	decNbLiveCObjects()
	return nil
}

func goReturnValues(result *C.ddwaf_result) (action Action, md []byte, err error) {
	if rc := result.action; rc == C.DDWAF_GOOD {
		return NoAction, nil, nil
	} else if rc < 0 {
		return NoAction, nil, goRunError(rc)
	}

	switch result.action {
	case C.DDWAF_MONITOR:
		action = MonitorAction
	case C.DDWAF_BLOCK:
		action = BlockAction
	}
	md = C.GoBytes(unsafe.Pointer(result.data), C.int(C.strlen(result.data)))
	return action, md, err
}

type RunError int

const (
	ErrInternal RunError = iota + 1
	ErrInvalidObject
	ErrInvalidArgument
	ErrTimeout
	ErrOutOfMemory
)

func (e RunError) Error() string {
	switch e {
	case ErrInternal:
		return "internal waf error"
	case ErrTimeout:
		return "waf timeout"
	case ErrInvalidObject:
		return "invalid waf object"
	case ErrInvalidArgument:
		return "invalid waf argument"
	case ErrOutOfMemory:
		return "out of memory"
	default:
		return fmt.Sprintf("unknown waf error %d", e)
	}
}

func goRunError(rc C.DDWAF_RET_CODE) error {
	switch rc {
	case C.DDWAF_ERR_INTERNAL:
		return ErrInternal
	case C.DDWAF_ERR_INVALID_OBJECT:
		return ErrInvalidObject
	case C.DDWAF_ERR_INVALID_ARGUMENT:
		return ErrInvalidArgument
	case C.DDWAF_ERR_TIMEOUT:
		return ErrTimeout
	default:
		return fmt.Errorf("unknown waf return code %d", int(rc))
	}
}
