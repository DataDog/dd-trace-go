package tracer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/encoding"
	"github.com/DataDog/sketches-go/ddsketch/mapping"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/spaolacci/murmur3"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"time"
)

// for now, use just a basic sketch
var sketchMapping, _ = mapping.NewLogarithmicMapping(0.01)

type dataPipeline struct {
	latencies []ddtrace.PipelineLatency
	callTime time.Time
	service string
	pipelineName string
}

func dataPipelineFromBaggage(data []byte, service string) (DataPipeline, error) {
	if len(data) < 8 {
		return nil, errors.New("data size too small")
	}
	pipeline := &dataPipeline{}
	t, err := encoding.DecodeVarint64(&data)
	if err != nil {
		return nil, err
	}
	pipeline.callTime = time.Unix(0, t*int64(time.Millisecond))
	for {
		if len(data) == 0 {
			return pipeline, nil
		}
		if len(data) < 8 {
			return nil, errors.New("message header less than 8 bytes")
		}
		hash := binary.LittleEndian.Uint64(data)
		data = data[8:]
		size, err := encoding.DecodeUvarint64(&data)
		if err != nil {
			return nil, err
		}
		if len(data) < int(size) {
			return nil, errors.New("message size less than size")
		}
		sketch, err := ddsketch.DecodeDDSketch(data[:size], store.BufferedPaginatedStoreConstructor, sketchMapping)
		if err != nil {
			return nil, err
		}
		pipeline.latencies = append(pipeline.latencies, ddtrace.PipelineLatency{Hash: hash, Summary: sketch})
		data = data[size:]
	}
}

func (p *dataPipeline) ToBaggage() ([]byte, error) {
	data := make([]byte, 0)
	hash := make([]byte, 8)
	encoding.EncodeVarint64(&data, p.callTime.UnixNano()/int64(time.Millisecond))
	for _, l := range p.latencies {
		// todo[piochelepiotr] Put size at the end so that we don't have to allocate that sketch memory twice.
		var sketch []byte
		l.Summary.Encode(&sketch, true)
		binary.LittleEndian.PutUint64(hash, l.Hash)
		data = append(data, hash...)
		encoding.EncodeUvarint64(&data, uint64(len(sketch)))
		data = append(data, sketch...)
	}
	if tracer, ok := internal.GetGlobalTracer().(*tracer); ok {
		tracer.config.statsd.Distribution("datadog.tracer.baggage_size", float64(len(data)), []string{fmt.Sprintf("service:%s", p.service)}, 1)
	}
	return data, nil
}

func (p *dataPipeline) GetCallTime() time.Time {
	return p.callTime
}

func (p *dataPipeline) GetLatencies() []ddtrace.PipelineLatency {
	return p.latencies
}

// MergeWith merges passed data pipelines into the current one. It returns the current data pipeline.
func (p *dataPipeline) MergeWith(receivingPipelineName string, dataPipelines ...DataPipeline) (DataPipeline, error) {
	// todo[piochelepiotr] Check what to do with summaries that are not copied.
	callTime := time.Now()
	pipelines := make([]DataPipeline, 0, len(dataPipelines)+1)
	pipelines = append(pipelines, p.SetCheckpoint(receivingPipelineName))
	for _, d := range dataPipelines {
		pipelines = append(pipelines, d.SetCheckpoint(receivingPipelineName))
	}
	latencies := make(map[uint64]*ddsketch.DDSketch)
	for _, pipeline := range pipelines {
		for _, l := range pipeline.GetLatencies() {
			if current, ok := latencies[l.Hash]; ok {
				if err := current.MergeWith(l.Summary); err != nil {
					return nil, err
				}
			} else {
				latencies[l.Hash] = l.Summary
			}
		}
	}
	merged := dataPipeline{latencies: make([]ddtrace.PipelineLatency, 0, len(latencies)), service: p.service, callTime: callTime}
	for hash, summary := range latencies {
		merged.latencies = append(merged.latencies, ddtrace.PipelineLatency{
			Hash: hash,
			Summary: summary,
		})
	}
	return &merged, nil
}

func newSummary() *ddsketch.DDSketch {
	// todo[piochelepiotr] Use paginated buffered store.
	return ddsketch.NewDDSketch(sketchMapping, store.BufferedPaginatedStoreConstructor(), store.BufferedPaginatedStoreConstructor())
}

func newDataPipeline(service string) *dataPipeline {
	summary := newSummary()
	summary.Add(0)
	now := time.Now()
	p := &dataPipeline{
		latencies: []ddtrace.PipelineLatency{
			{
				Hash: 0,
				Summary: summary,
			},
		},
		callTime: now,
		service: service,
	}
	return p.setCheckpoint("", now)
}

func nodeHash(service, receivingPipelineName string) uint64 {
	b := make([]byte, 0, len(service) + len(receivingPipelineName))
	b = append(b, service...)
	b = append(b, receivingPipelineName...)
	// todo[piochelepiotr] Using external library for that critical part is certainly not ideal.
	return murmur3.Sum64(b)
}

func pipelineHash(nodeHash, parentHash uint64) uint64 {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b, nodeHash)
	binary.LittleEndian.PutUint64(b[8:], parentHash)
	return murmur3.Sum64(b)
}

func (p *dataPipeline) SetCheckpoint(receivingPipelineName string) ddtrace.DataPipeline {
	return p.setCheckpoint(receivingPipelineName, time.Now())
}

func (p *dataPipeline) setCheckpoint(receivingPipelineName string, t time.Time) *dataPipeline {
	latency := float64(t.Sub(p.callTime)) / float64(time.Second)
	if latency < 0 {
		latency = 0
	}
	hash := nodeHash(p.service, receivingPipelineName)
	totalLatencies := make([]ddtrace.PipelineLatency, 0, len(p.latencies))
	for _, l := range p.latencies {
		var summary *ddsketch.DDSketch
		if latency == 0 {
			summary = l.Summary
		} else {
			summary = newSummary()
			l.Summary.ForEach(func(value, count float64) bool {
				summary.Add(value + latency)
				return false
			})
		}
		totalLatencies = append(totalLatencies, ddtrace.PipelineLatency{
			Summary: summary,
			Hash: pipelineHash(hash, l.Hash),
		})
	}
	if tracer, ok := internal.GetGlobalTracer().(*tracer); ok {
		for i, latency := range totalLatencies {
			log.Info(fmt.Sprintf("send point to stats aggregator service %s %d", p.service, latency.Hash))
			select {
			case tracer.pipelineStats.In <- pipelineStatsPoint{
				service: p.service,
				receivingPipelineName: receivingPipelineName,
				parentHash: p.latencies[i].Hash,
				pipelineHash: latency.Hash,
				timestamp: t.UnixNano(),
				summary: latency.Summary,
			}:
			default:
				log.Error("Pipeline stats channel full, disregarding stats point.")
			}
		}
	}
	d := dataPipeline{
		latencies: totalLatencies,
		callTime: t,
		service: p.service,
		pipelineName: receivingPipelineName,
	}
	return &d
}