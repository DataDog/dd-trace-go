package tracer

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

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
	assert.Equal(t, pipeline.callTime.Truncate(time.Millisecond).UnixNano(), convertedPipeline.GetCallTime().UnixNano())
	hash := convertedPipeline.GetHash()
	assert.Equal(t, pipeline.pipelineHash, hash)
}
