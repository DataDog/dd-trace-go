// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

// Queue pretends to be a networked message queue. In particular it arranges
// for calls Pull() to be blocked in a stack trace doing a net.Conn.Read().
type Queue struct {
	listener  net.Listener
	conn      net.Conn
	pushMutex sync.Mutex
	pullMutex sync.Mutex
}

func NewQueue() (q *Queue, err error) {
	q = &Queue{}
	q.listener, err = net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start TCP server: %s", err.Error())
	}

	go q.echoServer()

	q.conn, err = net.Dial("tcp", q.listener.Addr().String())
	if err != nil {
		return nil, fmt.Errorf("failed to dial TCP server: %s", err.Error())
	}

	return q, nil
}

func (q *Queue) echoServer() {
	conn, err := q.listener.Accept()
	if err != nil {
		log.Fatalf("failed to accept connection: %s\n", err.Error())
		return
	}
	defer conn.Close()

	if _, err := io.Copy(conn, conn); err != nil {
		log.Fatalf("failed to copy data: %s\n", err.Error())
		return
	}
}

func (q *Queue) Push(data []byte) error {
	q.pushMutex.Lock()
	defer q.pushMutex.Unlock()

	// Send the length of the message first
	err := binary.Write(q.conn, binary.BigEndian, uint64(len(data)))
	if err != nil {
		return fmt.Errorf("failed to send message length: %s", err.Error())
	}

	// Send the actual message
	_, err = q.conn.Write(data)
	if err != nil {
		return fmt.Errorf("failed to send message: %s", err.Error())
	}
	return nil
}

func (q *Queue) Pull() ([]byte, error) {
	q.pullMutex.Lock()
	defer q.pullMutex.Unlock()

	// Read the length of the message first
	var length uint64
	err := binary.Read(q.conn, binary.BigEndian, &length)
	if err != nil {
		return nil, fmt.Errorf("failed to read message length: %s", err.Error())
	}

	// Read the actual message
	data := make([]byte, length)
	_, err = io.ReadFull(q.conn, data)
	if err != nil {
		return nil, fmt.Errorf("failed to read message: %s", err.Error())
	}
	return data, nil
}

func (q *Queue) Close() {
	q.listener.Close()
	q.conn.Close()
}
