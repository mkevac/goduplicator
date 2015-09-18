package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	defaultBufferSize = 1024
	SPLICE_F_MOVE     = 1
	SPLICE_F_NONBLOCK = 2
	SPLICE_F_MORE     = 4
	SPLICE_F_GIFT     = 8
	MaxUint           = ^uint(0)
	MaxInt            = int(MaxUint >> 1)
)

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

func forwardZeroCopy(from net.Conn, to net.Conn, errCh chan error) {
	var (
		p       [2]int
		nullPtr *int64
	)

	err := unix.Pipe(p[:])
	if err != nil {
		errCh <- fmt.Errorf("pipe() error: %s", err)
		return
	}

	fromFile, err := from.(*net.TCPConn).File()
	if err != nil {
		errCh <- fmt.Errorf("error while creating File() from incoming connection: %s", err)
		return
	}

	toFile, err := to.(*net.TCPConn).File()
	if err != nil {
		errCh <- fmt.Errorf("error while creating File() from outgoing connection: %s", err)
		return
	}

	for {
		_, err = unix.Splice(int(fromFile.Fd()), nullPtr, p[1], nullPtr, MaxInt, SPLICE_F_MOVE)
		if err != nil {
			errCh <- fmt.Errorf("error while splicing from conn to pipe: %s", err)
			return
		}
		_, err = unix.Splice(p[0], nullPtr, int(toFile.Fd()), nullPtr, MaxInt, SPLICE_F_MOVE)
		if err != nil {
			errCh <- fmt.Errorf("error while splicing from pipe to conn: %s", err)
			return
		}
	}
}

func forwardAndZeroCopy(from net.Conn, to net.Conn, mirrors []net.Conn, errCh chan error) {
	var (
		p           [2]int
		nullPtr     *int64
		mirrorFiles []*os.File
		mirrorPipes [][2]int
	)

	err := unix.Pipe(p[:])
	if err != nil {
		errCh <- fmt.Errorf("pipe() error: %s", err)
		return
	}

	fromFile, err := from.(*net.TCPConn).File()
	if err != nil {
		errCh <- fmt.Errorf("error while creating File() from incoming connection: %s", err)
		return
	}

	toFile, err := to.(*net.TCPConn).File()
	if err != nil {
		errCh <- fmt.Errorf("error while creating File() from outgoing connection: %s", err)
		return
	}

	for _, m := range mirrors {
		mFile, err := m.(*net.TCPConn).File()
		if err != nil {
			errCh <- fmt.Errorf("error while creating File() from incoming connection: %s", err)
			return
		}
		mirrorFiles = append(mirrorFiles, mFile)

		var mPipe [2]int

		err = unix.Pipe(mPipe[:])
		if err != nil {
			errCh <- fmt.Errorf("pipe() error: %s", err)
			return
		}
		mirrorPipes = append(mirrorPipes, mPipe)

		go func(p int, f os.File) { // splice data from pipe to conn
			for {
				_, err = unix.Splice(p, nullPtr, int(f.Fd()), nullPtr, MaxInt, SPLICE_F_MOVE)
				if err != nil {
					errCh <- fmt.Errorf("error while splicing from pipe to conn: %s", err)
					return
				}
			}
		}(mPipe[0], *mFile)
	}

	for {
		_, err = unix.Splice(int(fromFile.Fd()), nullPtr, p[1], nullPtr, MaxInt, SPLICE_F_MOVE)
		if err != nil {
			errCh <- fmt.Errorf("error while splicing from conn to pipe: %s", err)
			return
		}

		nteed := int64(MaxInt)

		if len(mirrorPipes) > 0 {
			nteed, err = unix.Tee(p[0], mirrorPipes[0][1], MaxInt, SPLICE_F_MOVE)
			if err != nil {
				errCh <- fmt.Errorf("error while tee(): %s", err)
				return
			}
		}

		_, err = unix.Splice(p[0], nullPtr, int(toFile.Fd()), nullPtr, int(nteed), SPLICE_F_MOVE)
		if err != nil {
			errCh <- fmt.Errorf("error while splice(): %s", err)
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

func connect(origin net.Conn, forwarder net.Conn, mirrors []net.Conn, useZeroCopy bool, errCh chan error) {

	for i := 0; i < len(mirrors); i++ {
		go readAndDiscard(mirrors[i], errCh)
	}

	if useZeroCopy {
		go forwardZeroCopy(forwarder, origin, errCh)
		go forwardAndZeroCopy(origin, forwarder, mirrors, errCh)
	} else {
		go forward(forwarder, origin, errCh)
		go forwardAndCopy(origin, forwarder, mirrors, errCh)
	}

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
		useZeroCopy     bool
	)

	flag.BoolVar(&useZeroCopy, "z", false, "use zero copy")
	flag.StringVar(&listenAddress, "l", "", "listen address (e.g. 'localhost:8080')")
	flag.StringVar(&forwardAddress, "f", "", "forward to address (e.g. 'localhost:8081')")
	flag.Var(&mirrorAddresses, "m", "comma separated list of mirror addresses (e.g. 'localhost:8082,localhost:8083')")
	flag.Parse()

	if listenAddress == "" || forwardAddress == "" {
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
					log.Printf("error while connecting to mirror %s: %s", addr, err)
				} else {
					mirrorConns = append(mirrorConns, c)
				}
			}

			errCh := make(chan error, 1024)

			connect(c, cF, mirrorConns, useZeroCopy, errCh)

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
