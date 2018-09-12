package gorm_test

import (
	"log"

	"github.com/jinzhu/gorm"
	"github.com/lib/pq"

	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	gormtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/jinzhu/gorm"
)

func ExampleOpen() {
	// Register augments the provided driver with tracing, enabling it to be loaded by gormtrace.Open.
	sqltrace.Register("postgres", &pq.Driver{}, sqltrace.WithServiceName("my-service"))

	// Open the registered driver, allowing all uses of the returned *gorm.DB to be traced.
	db, err := gormtrace.Open("postgres", "postgres://pqgotest:password@localhost/pqgotest?sslmode=disable")
	defer db.Close()
	if err != nil {
		log.Fatal(err)
	}

	user := struct {
		gorm.Model
		Name string
	}{}

	// All calls through gorm.DB are now traced.
	db.Where("name = ?", "jinzhu").First(&user)
}
