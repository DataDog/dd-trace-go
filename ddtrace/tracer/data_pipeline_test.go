package tracer

import (
	"fmt"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/stretchr/testify/assert"
	"log"
	"math/rand"
	"testing"
	"time"
)

func TestDDSketch(t *testing.T) {
	s1 := ddsketch.NewDDSketch(sketchMapping, store.BufferedPaginatedStoreConstructor(), store.BufferedPaginatedStoreConstructor())
	for i := 0; i < 3000; i++ {
		value := rand.NormFloat64()+5
		if value < 0 {
			value = 0
		}
		value = float64(time.Unix(0, int64(value*float64(time.Second))).Truncate(time.Millisecond).UnixNano())/float64(time.Second)
		if err := s1.Add(value); err != nil {
			log.Printf("error adding value %v\n", err)
		}
		var data []byte
		s1.Encode(&data, true)
		fmt.Printf("%d, %d\n", i+1, len(data))
	}
}

func TestSerializeDataPipeline(t *testing.T) {
	now := time.Now()
	pipeline := dataPipeline{
		callTime: now,
		pipelineHash: 1,
	}
	data, err := pipeline.ToBaggage()
	assert.Nil(t, err)
	fmt.Printf("len of baggage is %d\n", len(data))
	tracer := tracer{config: &config{serviceName: "service"}}
	convertedPipeline, err := tracer.DataPipelineFromBaggage(data)
	assert.Nil(t, err)
	assert.Equal(t, pipeline.pipelineHash, convertedPipeline.GetHash())
	assert.Equal(t, pipeline.callTime.Truncate(time.Millisecond).UnixNano(), convertedPipeline.GetCallTime().UnixNano())
}
