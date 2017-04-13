package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	tracedsql "github.com/Datadog/dd-trace-go/tracer/contrib/sql"
	mysql "github.com/go-sql-driver/mysql"
)

func main() {
	tracedsql.NewTracedDriver("mysqlTraced", &mysql.MySQLDriver{}, nil)
	fmt.Printf("Drivers registered: %s", sql.Drivers())

	db, err := tracedsql.Open("mysqlTraced", "mysql", "root:3Z3ruyudg@tcp(127.0.0.1:3306)/employees")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	for {
		selectItems(db)
		time.Sleep(1 * time.Second)
	}
}

func selectItems(db *sql.DB) {
	var (
		emp_no     int
		first_name string
	)
	rows, err := db.Query("select emp_no, first_name from employees limit 4")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		err := rows.Scan(&emp_no, &first_name)
		if err != nil {
			log.Fatal(err)
		}
		log.Println(emp_no, first_name)
	}
	err = rows.Err()
	if err != nil {
		log.Fatal(err)
	}
}
