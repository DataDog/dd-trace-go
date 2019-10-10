// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package consul

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	consul "github.com/hashicorp/consul/api"
)

func BenchmarkKV(b *testing.B) {
	key := "test.key"
	pair := &consul.KVPair{Key: key, Value: []byte("test_value")}

	for name, testFunc := range map[string](func(k *consul.KV) error){
		"Put":    func(kv *consul.KV) error { _, err := kv.Put(pair, nil); return err },
		"Get":    func(kv *consul.KV) error { _, _, err := kv.Get(key, nil); return err },
		"List":   func(kv *consul.KV) error { _, _, err := kv.List(key, nil); return err },
		"Delete": func(kv *consul.KV) error { _, err := kv.Delete(key, nil); return err },
	} {
		b.Run(name, func(b *testing.B) {
			client, err := consul.NewClient(consul.DefaultConfig())
			if err != nil {
				b.FailNow()
			}
			kv := client.KV()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				err = testFunc(kv)
				if err != nil {
					b.FailNow()
				}
			}
		})
	}
}

func BenchmarkTracedKV(b *testing.B) {
	key := "test.key"
	pair := &consul.KVPair{Key: key, Value: []byte("test_value")}

	for name, testFunc := range map[string](func(k *KV) error){
		"Put":    func(kv *KV) error { _, err := kv.Put(pair, nil); return err },
		"Get":    func(kv *KV) error { _, _, err := kv.Get(key, nil); return err },
		"List":   func(kv *KV) error { _, _, err := kv.List(key, nil); return err },
		"Delete": func(kv *KV) error { _, err := kv.Delete(key, nil); return err },
	} {
		b.Run(name, func(b *testing.B) {
			tracer.Start()
			defer tracer.Stop()
			client, err := NewClient(consul.DefaultConfig())
			if err != nil {
				b.FailNow()
			}
			kv := client.KV()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				testFunc(kv)
			}
		})
	}
}
