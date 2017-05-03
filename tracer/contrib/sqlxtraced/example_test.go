package sqlxtraced

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func Example() {
	// You first have to register a traced version of the driver.
	// Make sure the `name` you register is equal to the original driver name.
	// Indeed it is due to the implementation of the BindType function of the sqlx package
	// which relies on hardcoded driver names.
	Register("postgres", &pq.Driver{}, nil)
	db, err := Open("postgres", "postgres://ubuntu@127.0.0.1:5432/circle_test?sslmode=disable", "postgres-test")

	// You can then use all the API of sqlx and all underlying sql calls will be traced.
	orgid := 2
	name := "master-db"
	sql, params, err := sqlx.In(`select meta from trace_services_metadata where org_id = ? and name = ?`,
		orgid, name)

	// This is especially the call to db.Rebind (and then BindType) that makes it impossible
	// to use the sqltraced package for tracing sqlx
	rows, err := db.Query(db.Rebind(sql), params...)
	if err != nil {
		log.Println(err)
	}
	defer rows.Close()

	var meta string
	for rows.Next() {
		err := rows.Scan(&meta)
		if err != nil {
			log.Fatal(err)
		}
		log.Println(meta)
	}
}
