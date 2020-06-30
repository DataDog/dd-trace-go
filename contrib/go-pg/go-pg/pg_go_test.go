package pggo

import (
	"context"
	"fmt"
	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"testing"
)


func GetPostgresConnection() *pg.DB{
	db := pg.Connect(&pg.Options{
		User:     "postgres",
		Password: "",
		Database: "postgres",
	})
	return db
}

func TestSelect(t *testing.T){
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	conn := GetPostgresConnection()
	Hook(conn)

	parentSpan, ctx := tracer.StartSpanFromContext(context.TODO(), "http.request",
		tracer.ServiceName("fake-http-server"),
		tracer.SpanType(ext.SpanTypeWeb),
	)

	var n int
	_, _ = conn.WithContext(ctx).QueryOne(pg.Scan(&n), "SELECT 1")
	parentSpan.Finish()
	spans := mt.FinishedSpans()

	assert.True(len(spans) >= 2)
	fmt.Println(spans[0].OperationName())
	assert.Equal("gopg", spans[0].OperationName())
	assert.Equal("http.request", spans[1].OperationName())
}
