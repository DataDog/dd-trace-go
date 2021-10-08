package main

import (
	"context"
	"fmt"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"time"
)

func main() {
	ctx := context.Background()
	tracer.Start(tracer.WithService("piotr-test-service"))
	defer tracer.Stop()
	_, ctx = tracer.SetDataPipelineCheckpointFromContext(ctx, "queue")
	dataPipeline, ok := tracer.DataPipelineFromContext(ctx)
	if ok {
		if baggage, err := dataPipeline.ToBaggage(); err == nil {
			convertedContext := context.Background()
			if pipeline, err := tracer.DataPipelineFromBaggage(baggage); err == nil {
				convertedContext = tracer.ContextWithDataPipeline(convertedContext, pipeline)
				time.Sleep(time.Second)
				fmt.Println("success passing context through baggage.")
				_, ctx = tracer.SetDataPipelineCheckpointFromContext(convertedContext, "queue2")
			}
		}
	}
}
