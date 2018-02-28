package gin_test

import (
	gintrace "github.com/DataDog/dd-trace-go/contrib/gin-gonic/gin"
	"github.com/DataDog/dd-trace-go/ddtrace/tracer"

	"github.com/gin-gonic/gin"
)

// To start tracing requests, add the trace middleware to your Gin router.
func Example() {
	// Create a gin.Engine
	r := gin.New()

	// Use the tracer middleware with your desired service name.
	r.Use(gintrace.Middleware("my-web-app"))

	// Set up some endpoints.
	r.GET("/hello", func(c *gin.Context) {
		c.String(200, "hello world!")
	})

	// And start gathering request traces.
	r.Run(":8080")
}

func ExampleHTML() {
	r := gin.Default()
	r.Use(gintrace.Middleware("my-web-app"))
	r.LoadHTMLGlob("templates/*")

	r.GET("/index", func(c *gin.Context) {
		// render the html and trace the execution time.
		gintrace.HTML(c, 200, "index.tmpl", gin.H{
			"title": "Main website",
		})
	})
}

func Example_spanFromContext() {
	r := gin.Default()
	r.Use(gintrace.Middleware("image-encoder"))
	r.GET("/image/encode", func(c *gin.Context) {
		ctx := c.Request.Context()
		// create a child span to track operation timing.
		encodeSpan, _ := tracer.StartSpanFromContext(ctx, "image.encode")
		// encode a image
		encodeSpan.Finish()

		c.String(200, "ok!")
	})

}
