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
	websocketTrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/websocket"
)

func ExampleEcho() {
	mux := muxtrace.NewRouter()

	var upgrader = websocketTrace.WrapUpgrader(websocket.Upgrader{})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Print("upgrade:", err)
			return
		}
		defer c.Close()

		// Websocket serve loop
		for {
			mt, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				break
			}
			log.Printf("recv: %s", message)

			err = c.WriteMessage(mt, message)
			if err != nil {
				log.Println("write:", err)
				break
			}
		}
	})

	log.Fatal(http.ListenAndServe(":8080", mux))
}
