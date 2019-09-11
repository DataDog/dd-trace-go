package consul

import (
	"testing"

	consul "github.com/hashicorp/consul/api"
)

func getKV(b *testing.B) *consul.KV {
	defer b.ResetTimer()

	client, err := consul.NewClient(consul.DefaultConfig())
	if err != nil {
		panic(err)
	}
	kv := client.KV()
	return kv
}

func getTracedKV(b *testing.B) *KV {
	defer b.ResetTimer()

	client, err := NewClient(consul.DefaultConfig())
	if err != nil {
		panic(err)
	}
	kv := client.KV()
	return kv
}

func BenchmarkKV_Put(b *testing.B) {
	kv := getKV(b)
	p := &consul.KVPair{Key: "test", Value: []byte("1000")}
	for i := 0; i < b.N; i++ {
		_, err := kv.Put(p, nil)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkTracedKV_Put(b *testing.B) {
	kv := getTracedKV(b)
	p := &consul.KVPair{Key: "test", Value: []byte("1000")}
	for i := 0; i < b.N; i++ {
		_, err := kv.Put(p, nil)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkKV_Get(b *testing.B) {
	kv := getKV(b)
	for i := 0; i < b.N; i++ {
		pair, _, err := kv.Get("test", nil)
		if err != nil {
			panic(err)
		}
		if pair == nil {
			panic(pair)
		}
	}
}

func BenchmarkTracedKV_Get(b *testing.B) {
	kv := getTracedKV(b)
	for i := 0; i < b.N; i++ {
		pair, _, err := kv.Get("test", nil)
		if err != nil {
			panic(err)
		}
		if pair == nil {
			panic(pair)
		}
	}
}
