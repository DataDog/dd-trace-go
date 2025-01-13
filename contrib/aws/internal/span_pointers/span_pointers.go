package span_pointers

import (
	"github.com/aws/smithy-go/middleware"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func HandleS3Operation(in middleware.DeserializeInput, out middleware.DeserializeOutput, span tracer.Span) {

}
