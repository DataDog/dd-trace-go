package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/mattn/go-sqlite3"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

const schema = `
create table if not exists colors (
	name text primary key,
	color text
);
PRAGMA journal_mode=WAL;
`

const update = `
insert into colors(name, color) values (?, ?)
on conflict(name) do update set color = excluded.color;
`

const query = `select color from colors where name = ?;`

func main() {
	tracer.Start(
		tracer.WithProfilerCodeHotspots(true),
		tracer.WithProfilerEndpoints(true),
	)
	defer tracer.Stop()

	if err := profiler.Start(); err != nil {
		log.Fatal("starting profile:", err)
	}
	defer profiler.Stop()
	addr := "localhost:8765"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}

	db, err := sql.Open("sqlite3", "colors.db")
	if err != nil {
		log.Fatal("opening:", err)
	}
	defer db.Close()

	if _, err := db.Exec(schema); err != nil {
		log.Fatal("creating:", err)
	}

	mux := httptrace.NewServeMux()
	mux.HandleFunc("/update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		v := r.URL.Query()
		name, color := v.Get("name"), v.Get("color")
		if name == "" || color == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		tx, err := db.Begin()
		if err != nil {
			log.Printf("begin tx failed: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		stmt, err := tx.Prepare(update)
		if err != nil {
			log.Printf("prepare tx failed: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer stmt.Close()
		if _, err = stmt.Exec(name, color); err != nil {
			log.Printf("stmt exec failed: %s", err)
			if err := tx.Rollback(); err != nil {
				log.Printf("rolling back transaction: %s", err)
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			log.Printf("stmt commit failed: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	})
	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		result := db.QueryRow(query, name)
		var color string
		err := result.Scan(&color)
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			return
		} else if err != nil {
			log.Printf("query failed: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "%s's favorite color is %s\n", name, color)
	})

	server := http.Server{
		Addr:    addr,
		Handler: mux,
	}
	go server.ListenAndServe()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	server.Close()
}
