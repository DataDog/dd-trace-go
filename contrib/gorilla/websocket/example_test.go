// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package websocket_test

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	muxtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"
	websockettrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/websocket"
)

// This example illustrates the usage of WrapConn to trace the websocket
// connections.
func ExampleWrapConn() {
	mux := muxtrace.NewRouter()

	var upgrader websocket.Upgrader
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		oconn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Error on upgrade: %v\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer oconn.Close()
		conn := websockettrace.WrapConn(r.Context(), oconn)
		for {
			mt, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Error reading message: %v\n", err)
				continue
			}
			log.Printf("Received message: %s\n", message)

			if err := conn.WriteMessage(mt, message); err != nil {
				log.Printf("Error writing message: %v\n", err)
				continue
			}
		}
	})
	log.Fatal(http.ListenAndServe(":8080", mux))
}
