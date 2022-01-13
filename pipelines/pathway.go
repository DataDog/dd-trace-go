package pipelines

import (
	"encoding/binary"
	"hash/fnv"
	"log"
	"math/rand"
	"time"
)

type Pathway struct {
	hash     uint64
	pathwayStart time.Time
	edgeStart time.Time
	service string
	edge    string
}

// Merge merges multiple pathways
func Merge(pathways []Pathway) Pathway {
	if len(pathways) == 0 {
		return Pathway{}
	}
	// for now, randomly select a pathway.
	n := rand.Intn(len(pathways))
	return pathways[n]
}

func nodeHash(service, edge string) uint64 {
	b := make([]byte, 0, len(service) + len(edge))
	b = append(b, service...)
	b = append(b, edge...)
	h := fnv.New64()
	h.Write(b)
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

func getService() string {
	if processor := getGlobalProcessor(); processor != nil && processor.service != "" {
		return processor.service
	}
	return "unnamed-go-service"
}

func NewPathway() Pathway {
	now := time.Now()
	p := Pathway{
		hash:     0,
		pathwayStart: now,
		edgeStart: now,
		service:  getService(),
	}
	return p.setCheckpoint("", now)
}

func (p Pathway) SetCheckpoint(edge string) Pathway {
	return p.setCheckpoint(edge, time.Now())
}

func (p Pathway) setCheckpoint(edge string, t time.Time) Pathway {
	child := Pathway{
		hash:         pathwayHash(nodeHash(p.service, edge), p.hash),
		pathwayStart: p.pathwayStart,
		edgeStart:    t,
		service:      p.service,
		edge:         edge,
	}
	if processor := getGlobalProcessor(); processor != nil {
		select {
		case processor.in <- statsPoint{
			service:        p.service,
			edge:           edge,
			parentHash:     p.hash,
			hash:           child.hash,
			timestamp:      t.UnixNano(),
			pathwayLatency: t.Sub(p.pathwayStart).Nanoseconds(),
			edgeLatency: t.Sub(p.edgeStart).Nanoseconds(),
		}:
		default:
			log.Println("Processor input channel full, disregarding stats point.")
		}
	}
	return child
}
