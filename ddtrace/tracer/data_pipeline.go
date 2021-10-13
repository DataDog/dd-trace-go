package tracer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/mapping"
	"github.com/DataDog/sketches-go/ddsketch/pb/sketchpb"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/golang/protobuf/proto"
	"github.com/spaolacci/murmur3"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"time"
	// "github.com/spaolacci/murmur3"
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
	pipeline.callTime = time.Unix(0, int64(binary.LittleEndian.Uint64(data)))
	data = data[8:]
	for {
		if len(data) == 0 {
			return pipeline, nil
		}
		fmt.Printf("len of data %d\n", len(data))
		if len(data) < 12 {
			return nil, errors.New("message header less than 12 bytes")
		}
		hash := binary.LittleEndian.Uint64(data)
		fmt.Printf("hash %d\n", hash)
		data = data[8:]
		size := binary.LittleEndian.Uint32(data)
		fmt.Printf("size %d\n", size)
		data = data[4:]
		if len(data) < int(size) {
			return nil, errors.New("message size less than size")
		}
		var pb sketchpb.DDSketch
		err := proto.Unmarshal(data[:size], &pb)
		if err != nil {
			return nil, err
		}
		summary, err := ddsketch.FromProto(&pb)
		if err != nil {
			return nil, err
		}
		pipeline.latencies = append(pipeline.latencies, ddtrace.PipelineLatency{Hash: hash, Summary: summary})
		data = data[size:]
	}
	return pipeline, nil
}

func (p *dataPipeline) ToBaggage() ([]byte, error) {
	data := make([]byte, 8)
	hash := make([]byte, 8)
	size := make([]byte, 4)
	binary.LittleEndian.PutUint64(data, uint64(p.callTime.UnixNano()))
	for _, l := range p.latencies {
		sketch, err := proto.Marshal(l.Summary.ToProto())
		if err != nil {
			return nil, err
		}
		binary.LittleEndian.PutUint64(hash, l.Hash)
		binary.LittleEndian.PutUint32(size, uint32(len(sketch)))
		data = append(data, hash...)
		data = append(data, size...)
		data = append(data, sketch...)
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
	return ddsketch.NewDDSketch(sketchMapping, store.NewCollapsingLowestDenseStore(1000), store.NewCollapsingLowestDenseStore(1000))
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