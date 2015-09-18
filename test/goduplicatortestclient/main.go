package main

import (
	"bufio"
	"bytes"
	"flag"
	"sync/atomic"
	"time"
)
import "log"
import "net"

var (
	serverAddress string
	parallel      uint
	requests      uint64
	messageSize   uint
)

func client() {
	c, err := net.Dial("tcp", serverAddress)
	if err != nil {
		log.Fatalf("error while connecting to server: %s", err)
	}

	r := bufio.NewReader(c)

	bufferToSend := bytes.Repeat([]byte("a"), int(messageSize-1))
	bufferToSend = append(bufferToSend, '\n')

	for {
		_, err := c.Write(bufferToSend)
		if err != nil {
			log.Fatalf("error while sending to server: %s", err)
		}

		_, err = r.ReadBytes('\n')
		if err != nil {
			log.Fatalf("error while reading from server: %s", err)
		}

		atomic.AddUint64(&requests, 1)
	}
}

func main() {

	flag.UintVar(&messageSize, "s", 1024, "message size in bytes")
	flag.StringVar(&serverAddress, "a", ":11000", "server address to connect to")
	flag.UintVar(&parallel, "p", 10, "how many parallel connections")
	flag.Parse()

	for i := uint(0); i < parallel; i++ {
		go client()
	}

	for {
		oldValue := atomic.LoadUint64(&requests)
		select {
		case <-time.Tick(time.Second):
			newValue := atomic.LoadUint64(&requests)
			log.Printf("%v req/sec", newValue-oldValue)
			oldValue = newValue
		}
	}
}
