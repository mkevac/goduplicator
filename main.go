package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"strings"
)

const defaultBufferSize = 1024

func readAndDiscard(c net.Conn, errCh chan error) {
	for {
		var b [defaultBufferSize]byte
		_, err := c.Read(b[:])
		if err != nil {
			errCh <- err
			return
		}
	}
}

func forward(from net.Conn, to net.Conn, errCh chan error) {
	for {
		var b [defaultBufferSize]byte

		n, err := from.Read(b[:])
		if err != nil {
			errCh <- err
			return
		}

		_, err = to.Write(b[:n])
		if err != nil {
			errCh <- err
			return
		}
	}
}

func forwardAndCopy(from net.Conn, to net.Conn, mirrors []net.Conn, errCh chan error) {
	for {
		var b [defaultBufferSize]byte

		n, err := from.Read(b[:])
		if err != nil {
			errCh <- err
			return
		}

		_, err = to.Write(b[:n])
		if err != nil {
			errCh <- err
			return
		}

		for i := 0; i < len(mirrors); i++ {
			_, err = mirrors[i].Write(b[:n])
			if err != nil {
				errCh <- err
				return
			}
		}
	}
}

func connect(origin net.Conn, forwarder net.Conn, mirrors []net.Conn, errCh chan error) {

	for i := 0; i < len(mirrors); i++ {
		go readAndDiscard(mirrors[i], errCh)
	}

	go forward(forwarder, origin, errCh)
	go forwardAndCopy(origin, forwarder, mirrors, errCh)
}

type mirrorList []string

func (l *mirrorList) String() string {
	return fmt.Sprint(*l)
}

func (l *mirrorList) Set(value string) error {
	for _, m := range strings.Split(value, ",") {
		*l = append(*l, m)
	}
	return nil
}

func main() {

	var (
		listenAddress   string
		forwardAddress  string
		mirrorAddresses mirrorList
	)

	flag.StringVar(&listenAddress, "l", "", "listen address (e.g. 'localhost:8080')")
	flag.StringVar(&forwardAddress, "f", "", "forward to address (e.g. 'localhost:8081')")
	flag.Var(&mirrorAddresses, "m", "comma separated list of mirror addresses (e.g. 'localhost:8082,localhost:8083')")
	flag.Parse()

	if listenAddress == "" || forwardAddress == "" || len(mirrorAddresses) == 0 {
		flag.Usage()
		return
	}

	l, err := net.Listen("tcp", listenAddress)
	if err != nil {
		log.Fatalf("error while listening: %s", err)
	}

	connNo := uint64(1)

	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatalf("Error while accepting: %s", err)
		}

		log.Printf("accepted connection %d (%s <-> %s)", connNo, c.RemoteAddr(), c.LocalAddr())

		go func(c net.Conn) {

			cF, err := net.Dial("tcp", forwardAddress)
			if err != nil {
				log.Fatalf("error while connecting to forwarder: %s", err)
			}

			mirrorConns := make([]net.Conn, 0)

			for _, addr := range mirrorAddresses {
				c, err := net.Dial("tcp", addr)
				if err != nil {
					log.Fatalf("error while connecting to mirror %s: %s", addr, err)
				}
				mirrorConns = append(mirrorConns, c)
			}

			errCh := make(chan error, 1024)

			connect(c, cF, mirrorConns, errCh)

			err = <-errCh

			log.Printf("got error: %s", err)

			c.Close()
			cF.Close()

			for _, c := range mirrorConns {
				c.Close()
			}
		}(c)

		connNo += 1
	}
}
