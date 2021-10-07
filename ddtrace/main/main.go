package main

import (
	"context"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"time"
)

func main() {
	ctx := context.Background()
	tracer.Start(tracer.WithService("piotr-test-service"))
	defer tracer.Stop()
	_, ctx = tracer.SetDataPipelineCheckpointFromContext(ctx, "queue")
	time.Sleep(time.Second)
	_, ctx = tracer.SetDataPipelineCheckpointFromContext(ctx, "queue2")
}
