// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package redigo

import (
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"unsafe"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	garyburdredis "github.com/garyburd/redigo/redis"
	gomoduleredis "github.com/gomodule/redigo/redis"
)

// RedisDialConfig contains the configurable parameters for the Redis connection.
type RedisDialConfig struct {
	DB int
}

const (
	structFieldNameDialOptionFunc = "f"
	structFieldNameConfigDBIndex  = "db"
)

// DialOption is a type constraint for the redis.DialOption struct from both redigo integrations.
type DialOption interface {
	garyburdredis.DialOption | gomoduleredis.DialOption
}

// ResolveDialOpts resolves the config from the given []redis.DialOption.
// Since both the config struct (redis.dialOption) and the redis.DialOption.f struct field are unexported,
// this function uses reflection to initialize and access the struct fields.
func ResolveDialOpts[T DialOption](opts []T) (*RedisDialConfig, bool) {
	if len(opts) == 0 {
		return &RedisDialConfig{}, true
	}
	defer func() {
		if err := recover(); err != nil {
			log.Debug("contrib/internal/redigo: Failed to resolve dial options")
		}
	}()
	cfg := &RedisDialConfig{}
	redisDoVal := newRedisDialOptionValue(opts[0])
	for _, opt := range opts {
		funcVal := reflect.ValueOf(&opt).Elem().FieldByName(structFieldNameDialOptionFunc)
		callableFuncVal := reflect.NewAt(funcVal.Type(), unsafe.Pointer(funcVal.UnsafeAddr())).Elem()
		callArgs := []reflect.Value{redisDoVal}
		_ = callableFuncVal.Call(callArgs)
	}
	cfg.DB = int(redisDoVal.Elem().FieldByName(structFieldNameConfigDBIndex).Int())
	return cfg, true
}

// newRedisDialOptionValue initializes the unexported *redis.dialOptions struct from redigo/redis.
// This is equivalent to doing the following: return &redis.dialOptions{}
func newRedisDialOptionValue[T DialOption](opt T) reflect.Value {
	fVal := reflect.ValueOf(&opt).Elem().FieldByName(structFieldNameDialOptionFunc)
	firstArgType := fVal.Type().In(0)
	firstArgPtrVal := reflect.New(firstArgType).Elem()
	s := firstArgPtrVal.Type().Elem()
	return reflect.New(s)
}

var pathDBRegexp = regexp.MustCompile(`/(\d*)\z`)

// GetDBIndexFromURL extracts the DB Index from the given Redis connection URL. The URLs should follow the draft IANA
// specification for the scheme (https://www.iana.org/assignments/uri-schemes/prov/redis).
// Example: redis://user:secret@localhost:6379/0?foo=bar&qux=baz
func GetDBIndexFromURL(rawURL string) (int, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, false
	}
	if u.Path == "" {
		return 0, true
	}
	match := pathDBRegexp.FindStringSubmatch(u.Path)
	if len(match) == 2 {
		if len(match[1]) > 0 {
			if db, err := strconv.Atoi(match[1]); err == nil {
				return db, true
			}
		}
	}
	return 0, false
}
