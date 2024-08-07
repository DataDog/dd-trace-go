// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import "sync"

type metaMapPool struct {
	*sync.Pool
}

var mmp = &metaMapPool{
	Pool: &sync.Pool{
		New: func() any {
			return defaultMetaMap()
		},
	},
}

func (p *metaMapPool) Get() map[string]string {
	return p.Pool.Get().(map[string]string)
}

func (p *metaMapPool) Put(m map[string]string) {
	if m == nil {
		return
	}
	clear(m)
	p.Pool.Put(m)
}

func releaseSpanMaps(spans []*span) {
	for i := range spans {
		releaseSpanMap(spans[i])
	}
}

func releaseSpanMap(s *span) {
	mmp.Put(s.Meta)
	s.Meta = nil
}
