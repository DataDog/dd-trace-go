// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

var hashableEdgeTags = map[string]struct{}{"event_type": {}, "exchange": {}, "group": {}, "topic": {}, "type": {}, "direction": {}}

// Pathway is used to monitor how payloads are sent across different services.
// An example Pathway would be:
// service A -- edge 1 --> service B -- edge 2 --> service C
// So it's a branch of services (we also call them "nodes") connected via edges.
// As the payload is sent around, we save the start time (start of service A),
// and the start time of the previous service.
// This allows us to measure the latency of each edge, as well as the latency from origin of any service.
type Pathway struct {
	// hash is the hash of the current node, of the parent node, and of the edge that connects the parent node
	// to this node.
	hash uint64
	// pathwayStart is the start of the first node in the Pathway
	pathwayStart time.Time
	// edgeStart is the start of the previous node.
	edgeStart time.Time
}

// Merge merges multiple pathways into one.
// The current implementation samples one resulting Pathway. A future implementation could be more clever
// and actually merge the Pathways.
func Merge(pathways []Pathway) Pathway {
	if len(pathways) == 0 {
		return Pathway{}
	}
	// Randomly select a pathway to propagate downstream.
	n := rand.Intn(len(pathways))
	return pathways[n]
}

func isWellFormedEdgeTag(t string) bool {
	if i := strings.IndexByte(t, ':'); i != -1 {
		if j := strings.LastIndexByte(t, ':'); j == i {
			if _, exists := hashableEdgeTags[t[:i]]; exists {
				return true
			}
		}
	}
	return false
}

func nodeHash(service, env, primaryTag string, edgeTags []string) uint64 {
	h := fnv.New64()
	sort.Strings(edgeTags)
	fmt.Printf("service %s, env %s, primary tag %s, edge tags %v\n", service, env, primaryTag, edgeTags)
	h.Write([]byte(service))
	h.Write([]byte(env))
	h.Write([]byte(primaryTag))
	for _, t := range edgeTags {
		if isWellFormedEdgeTag(t) {
			h.Write([]byte(t))
		} else {
			fmt.Println("not formatted correctly", t)
		}
	}
	return h.Sum64()
}

func pathwayHash(nodeHash, parentHash uint64) uint64 {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b, nodeHash)
	binary.LittleEndian.PutUint64(b[8:], parentHash)
	h := fnv.New64()
	h.Write(b)
	return h.Sum64()
}

// NewPathway creates a new pathway.
func NewPathway(edgeTags ...string) Pathway {
	return newPathway(time.Now(), edgeTags...)
}

func newPathway(now time.Time, edgeTags ...string) Pathway {
	p := Pathway{
		hash:         0,
		pathwayStart: now,
		edgeStart:    now,
	}
	return p.setCheckpoint(now, edgeTags)
}

// SetCheckpoint sets a checkpoint on a pathway.
func (p Pathway) SetCheckpoint(edgeTags ...string) Pathway {
	return p.setCheckpoint(time.Now(), edgeTags)
}

func (p Pathway) setCheckpoint(now time.Time, edgeTags []string) Pathway {
	aggr := getGlobalAggregator()
	service := defaultServiceName
	primaryTag := ""
	env := ""
	if aggr != nil {
		service = aggr.service
		primaryTag = aggr.primaryTag
		env = aggr.env
	}
	child := Pathway{
		hash:         pathwayHash(nodeHash(service, env, primaryTag, edgeTags), p.hash),
		pathwayStart: p.pathwayStart,
		edgeStart:    now,
	}
	if aggregator := getGlobalAggregator(); aggregator != nil {
		select {
		case aggregator.in <- statsPoint{
			edgeTags:       edgeTags,
			parentHash:     p.hash,
			hash:           child.hash,
			timestamp:      now.UnixNano(),
			pathwayLatency: now.Sub(p.pathwayStart).Nanoseconds(),
			edgeLatency:    now.Sub(p.edgeStart).Nanoseconds(),
		}:
		default:
			atomic.AddInt64(&aggregator.stats.dropped, 1)
		}
	}
	return child
}
