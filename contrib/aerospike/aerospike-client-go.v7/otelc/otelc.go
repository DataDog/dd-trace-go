// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package otelc holds the otelc rules for aerospike/aerospike-client-go.v7. See otelc.yaml.
package otelc

import (
	"context"
	"reflect"
	"sync"

	as "github.com/aerospike/aerospike-client-go/v7"

	aerospike "github.com/DataDog/dd-trace-go/contrib/aerospike/aerospike-client-go.v7/v2"
)

// WrapReceiver returns a traced client bound to ctx for recv, the receiver of
// a Client method call that the otelc.yaml rules rewrite. It is not meant to
// be called directly.
//
// recv is a *as.Client, an as.Client, or a value of a type that embeds one,
// since otelc also matches a Client method promoted through an embedded field.
func WrapReceiver(recv any, ctx context.Context) *aerospike.Client {
	switch c := recv.(type) {
	case *as.Client:
		return aerospike.WrapClientContext(c, ctx)
	case as.Client:
		return aerospike.WrapClientContext(c, ctx)
	}
	return aerospike.WrapClientContext(embeddedClient(recv), ctx)
}

var (
	clientType    = reflect.TypeFor[as.Client]()
	clientPtrType = reflect.TypeFor[*as.Client]()

	// clientPaths caches clientPath by receiver type.
	clientPaths sync.Map // reflect.Type -> []int
)

// embeddedClient returns the *as.Client that a method promoted to recv's type
// runs on. A nil pointer on the way gives a nil client, and the method call
// then panics as the original call would.
func embeddedClient(recv any) *as.Client {
	v := reflect.ValueOf(recv)
	if v.Kind() != reflect.Pointer {
		// A value receiver is a copy, as for an as.Client value. Copy it
		// once more to make its fields addressable.
		cp := reflect.New(v.Type())
		cp.Elem().Set(v)
		v = cp
	}
	path, ok := clientPaths.Load(v.Type())
	if !ok {
		path, _ = clientPaths.LoadOrStore(v.Type(), clientPath(v.Type()))
	}
	for _, i := range path.([]int) {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return nil
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	switch v.Type() {
	case clientPtrType:
		return v.Interface().(*as.Client)
	case clientType:
		return v.Addr().Interface().(*as.Client)
	}
	return nil
}

// clientPath returns the indexes of the embedded fields that lead from t to
// its embedded as.Client or *as.Client. It searches level by level, as Go
// resolves a promoted method, so the shallowest one wins.
func clientPath(t reflect.Type) []int {
	type node struct {
		t    reflect.Type
		path []int
	}
	level := []node{{t: t}}
	seen := map[reflect.Type]bool{}
	for len(level) > 0 {
		var next []node
		for _, n := range level {
			st := n.t
			if st.Kind() == reflect.Pointer {
				st = st.Elem()
			}
			if st.Kind() != reflect.Struct || seen[st] {
				continue
			}
			seen[st] = true
			for i := range st.NumField() {
				f := st.Field(i)
				if !f.Anonymous {
					continue
				}
				path := append(append([]int(nil), n.path...), i)
				if f.Type == clientType || f.Type == clientPtrType {
					return path
				}
				next = append(next, node{t: f.Type, path: path})
			}
		}
		level = next
	}
	return nil
}
