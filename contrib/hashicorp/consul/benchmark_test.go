package consul

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	consul "github.com/hashicorp/consul/api"
)

func BenchmarkKV(b *testing.B) {
	key := "test.key"
	pair := &consul.KVPair{Key: key, Value: []byte("test_value")}
	testCases := []struct {
		f    func(k *consul.KV) error
		name string
	}{
		{func(kv *consul.KV) error { _, err := kv.Put(pair, nil); return err }, "Put"},
		{func(kv *consul.KV) error { _, _, err := kv.Get(key, nil); return err }, "Get"},
		{func(kv *consul.KV) error { _, _, err := kv.List(key, nil); return err }, "List"},
		{func(kv *consul.KV) error { _, err := kv.Delete(key, nil); return err }, "Delete"},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			mt := mocktracer.Start()
			defer mt.Stop()
			client, err := consul.NewClient(consul.DefaultConfig())
			if err != nil {
				b.FailNow()
			}
			kv := client.KV()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				err = tc.f(kv)
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
	testCases := []struct {
		f    func(k *KV) error
		name string
	}{
		{func(kv *KV) error { _, err := kv.Put(pair, nil); return err }, "Put"},
		{func(kv *KV) error { _, _, err := kv.Get(key, nil); return err }, "Get"},
		{func(kv *KV) error { _, _, err := kv.List(key, nil); return err }, "List"},
		{func(kv *KV) error { _, err := kv.Delete(key, nil); return err }, "Delete"},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			tracer.Start()
			defer tracer.Stop()
			client, err := NewClient(consul.DefaultConfig())
			if err != nil {
				b.FailNow()
			}
			kv := client.KV()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				tc.f(kv)
			}
		})
	}
}
