// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"context"
	"testing"

	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ptrStore struct {
	*as.Client
}

type valueStore struct {
	inner
}

type inner struct {
	as.Client
}

type nested struct {
	*ptrStore
}

// shallow has a Client at depth 1 and another at depth 2. Go promotes the
// methods of the one at depth 1.
type shallow struct {
	*as.Client
	ptrStore
}

func TestWrapReceiver(t *testing.T) {
	client := &as.Client{DefaultPolicy: as.NewPolicy()}
	other := &as.Client{DefaultPolicy: as.NewPolicy()}

	t.Run("pointer", func(t *testing.T) {
		assert.Same(t, client, WrapReceiver(client, context.Background()).Client)
	})
	t.Run("value", func(t *testing.T) {
		got := WrapReceiver(*client, context.Background()).Client
		require.NotNil(t, got)
		assert.NotSame(t, client, got)
		assert.Same(t, client.DefaultPolicy, got.DefaultPolicy)
	})
	t.Run("embedded pointer", func(t *testing.T) {
		assert.Same(t, client, WrapReceiver(ptrStore{client}, context.Background()).Client)
		assert.Same(t, client, WrapReceiver(&ptrStore{client}, context.Background()).Client)
	})
	t.Run("embedded value behind a pointer", func(t *testing.T) {
		vs := &valueStore{inner{*client}}
		assert.Same(t, &vs.Client, WrapReceiver(vs, context.Background()).Client)
	})
	t.Run("embedded value", func(t *testing.T) {
		vs := valueStore{inner{*client}}
		got := WrapReceiver(vs, context.Background()).Client
		require.NotNil(t, got)
		assert.NotSame(t, &vs.Client, got)
		assert.Same(t, client.DefaultPolicy, got.DefaultPolicy)
	})
	t.Run("nested", func(t *testing.T) {
		assert.Same(t, client, WrapReceiver(nested{&ptrStore{client}}, context.Background()).Client)
	})
	t.Run("nil on the way", func(t *testing.T) {
		assert.Nil(t, WrapReceiver(nested{}, context.Background()).Client)
		assert.Nil(t, WrapReceiver((*ptrStore)(nil), context.Background()).Client)
	})
	t.Run("shallowest wins", func(t *testing.T) {
		assert.Same(t, client, WrapReceiver(shallow{client, ptrStore{other}}, context.Background()).Client)
	})
}
