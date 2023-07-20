// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package consul

import (
	"testing"

	"github.com/stretchr/testify/require"

	consul "github.com/hashicorp/consul/api"
	consultrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/hashicorp/consul"
)

type Integration struct {
	client   *consultrace.Client
	numSpans int
	opts     []consultrace.ClientOption
}

func New() *Integration {
	return &Integration{
		opts: make([]consultrace.ClientOption, 0),
	}
}

func (i *Integration) Name() string {
	return "hashicorp/consul"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	var err error
	i.client, err = consultrace.NewClient(consul.DefaultConfig(), i.opts...)
	require.NoError(t, err)

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	key := "test.key"
	pair := &consul.KVPair{Key: key, Value: []byte("test_value")}
	for _, testFunc := range map[string]func(kv *consultrace.KV){
		"Put":  func(kv *consultrace.KV) { kv.Put(pair, nil) },
		"Get":  func(kv *consultrace.KV) { kv.Get(key, nil) },
		"List": func(kv *consultrace.KV) { kv.List(key, nil) },
	} {
		kv := i.client.KV()
		testFunc(kv)
		i.numSpans++
	}
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, consultrace.WithServiceName(name))
}
