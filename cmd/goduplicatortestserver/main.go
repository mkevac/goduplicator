package main

import (
	"bufio"
	"flag"
	"log"
	"net"
)

func handleConnection(c net.Conn) {
	r := bufio.NewReader(c)

	for {
		l, err := r.ReadBytes('\n')
		if err != nil {
			c.Close()
			log.Printf("error while reading line: %s", err)
			return
		}
		_, err = c.Write(l)
		if err != nil {
			c.Close()
			log.Fatalf("error while writing: %s", err)
			return
		}
	}
}

func main() {
	var (
		listeningAddress string
	)

	flag.StringVar(&listeningAddress, "l", ":11000", "listening address")
	flag.Parse()

	l, err := net.Listen("tcp", listeningAddress)
	if err != nil {
		log.Fatalf("error while listening: %s", err)
	}

	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatalf("error while accepting: %s", err)
		}

		go handleConnection(c)
	}
}
