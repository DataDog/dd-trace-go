package sql

import (
	"testing"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/stretchr/testify/assert"
)

func TestSelect(t *testing.T) {
	assert := assert.New(t)
	testTracer, testTransport := getTestTracer()
	testTracer.DebugLoggingEnabled = debug

	db, err := sql.Open("mysql", "mysql-test", "root:3Z3ruyudg@tcp(127.0.0.1:3306)/employees")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("select emp_no, first_name from employees limit 5")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	//// verify traces look good
	//assert.Nil(testTracer.FlushTraces())
	//traces := testTransport.Traces()
	//assert.Len(traces, 1)
	//spans := traces[0]
	//assert.Len(spans, 1)
	//if len(spans) < 1 {
	//	t.Fatalf("no spans")
	//}
	//s := spans[0]
	//assert.Equal(s.Service, "foobar")
	//assert.Equal(s.Name, "gin.request")
	//// FIXME[matt] would be much nicer to have "/user/:id" here
	//assert.True(strings.Contains(s.Resource, "gintrace.TestTrace200"))
	//assert.Equal(s.GetMeta("test.gin"), "ginny")
	//assert.Equal(s.GetMeta("http.status_code"), "200")
	//assert.Equal(s.GetMeta("http.method"), "GET")
	//assert.Equal(s.GetMeta("http.url"), "/user/123")
}

// getTestTracer returns a Tracer with a DummyTransport
func getTestTracer() (*tracer.Tracer, *dummyTransport) {
	transport := &dummyTransport{}
	tracer := tracer.NewTracerTransport(transport)
	return tracer, transport
}
