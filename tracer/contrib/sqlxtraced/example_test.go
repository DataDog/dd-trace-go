package sqlxtraced

import (
	"log"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func Example() {
	Register("postgres", "postgres-test", &pq.Driver{}, nil)
	db, err := Open("postgres", "postgres://ubuntu@127.0.0.1:5432/circle_test?sslmode=disable")

	orgid := 2
	name := "master-db"
	sql, params, err := sqlx.In(`select meta from trace_services_metadata where org_id = ? and name = ?`,
		orgid, name)

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
