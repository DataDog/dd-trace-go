package config

import (
	"math/rand"
	"sync/atomic"
)

type Config struct {
	rand int
}

var globalConfig atomic.Value

func Global() *Config {
	v := globalConfig.Load()
	if v == nil || useFreshConfig.Load() {
		cfg := &Config{rand: rand.Intn(1000)}
		globalConfig.Store(cfg)
		return cfg
	}
	return v.(*Config)
}

var useFreshConfig atomic.Bool

func SetUseFreshConfig(use bool) {
	useFreshConfig.Store(use)
}
