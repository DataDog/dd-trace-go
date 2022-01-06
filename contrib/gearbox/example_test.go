package gearbox

import (
	"log"

	"github.com/gogearbox/gearbox"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Example() {

	// Start the tracer
	tracer.Start()
	defer tracer.Stop()

	gb := gearbox.New()
	gb.Use(Datadog)

	err := gb.Start(":8080")
	if err != nil {
		log.Fatal(err)
	}

}
