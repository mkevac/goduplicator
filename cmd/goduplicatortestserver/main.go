package main

import (
	"bufio"
	"flag"
	"io"
	"log"
	"net"
	"sync/atomic"
	"time"
)

func statsPrinter() {
	var rSaved uint64 = 0

	for range time.Tick(time.Second) {
		c := atomic.LoadUint64(&connections)
		r := atomic.LoadUint64(&requests)

		log.Printf("%v c, %v rps", c, r-rSaved)
		rSaved = r
	}
}

func handleConnection(c net.Conn) {
	atomic.AddUint64(&connections, 1)
	defer atomic.AddUint64(&connections, ^uint64(1))
	defer c.Close()

	r := bufio.NewReader(c)

	for {
		l, err := r.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("error while reading line: %s", err)
			}
			return
		}

		atomic.AddUint64(&requests, 1)

		_, err = c.Write(l)
		if err != nil {
			log.Printf("error while writing: %s", err)
			return
		}
	}
}

var listeningAddress string
var connections uint64
var requests uint64

func main() {
	flag.StringVar(&listeningAddress, "l", ":11000", "listening address")
	flag.Parse()

	l, err := net.Listen("tcp", listeningAddress)
	if err != nil {
		log.Fatalf("error while listening: %s", err)
	}

	go statsPrinter()

	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatalf("error while accepting: %s", err)
		}

		go handleConnection(c)
	}
}
