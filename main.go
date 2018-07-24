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

type mirror struct {
	addr   string
	conn   net.Conn
	closed bool
}

func readAndDiscard(m mirror, errCh chan error) {
	for {
		var b [defaultBufferSize]byte
		_, err := m.conn.Read(b[:])
		if err != nil {
			m.conn.Close()
			m.closed = true
			select {
			case errCh <- err:
			default:
			}
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
		log.Fatalf("pipe() error: %s", err)
	}

	fromFile, err := from.(*net.TCPConn).File()
	if err != nil {
		log.Fatalf("error while creating File() from incoming connection: %s", err)
	}

	toFile, err := to.(*net.TCPConn).File()
	if err != nil {
		log.Fatalf("error while creating File() from outgoing connection: %s", err)
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

func forwardAndZeroCopy(from net.Conn, to net.Conn, mirrors []mirror, errChForwardee, errChMirrors chan error) {
	type mirrorInt struct {
		mirror
		mirrorFile *os.File
		mirrorPipe [2]int
	}

	var (
		p          [2]int
		nullPtr    *int64
		mirrorsInt []mirrorInt
	)

	err := unix.Pipe(p[:])
	if err != nil {
		log.Fatalf("pipe() error: %s", err)
	}

	fromFile, err := from.(*net.TCPConn).File()
	if err != nil {
		log.Fatalf("error while creating File() from incoming connection: %s", err)
	}

	toFile, err := to.(*net.TCPConn).File()
	if err != nil {
		log.Fatalf("error while creating File() from outgoing connection: %s", err)
	}

	for _, m := range mirrors {
		mFile, err := m.conn.(*net.TCPConn).File()
		if err != nil {
			log.Fatalf("error while creating File() from incoming connection: %s", err)
		}

		var mPipe [2]int

		err = unix.Pipe(mPipe[:])
		if err != nil {
			log.Fatalf("pipe() error: %s", err)
		}

		mirrorsInt = append(mirrorsInt, mirrorInt{
			mirror:     m,
			mirrorPipe: mPipe,
			mirrorFile: mFile,
		})
	}

	for _, m := range mirrorsInt {

		go func(m mirrorInt) { // splice data from pipe to conn
			for {
				_, err = unix.Splice(m.mirrorPipe[0], nullPtr, int(m.mirrorFile.Fd()), nullPtr, MaxInt, SPLICE_F_MOVE)
				if err != nil {
					select {
					case errChMirrors <- fmt.Errorf("error while splicing from pipe to conn: %s", err):
					default:
					}
					return
				}
			}
		}(m)
	}

	for {
		_, err = unix.Splice(int(fromFile.Fd()), nullPtr, p[1], nullPtr, MaxInt, SPLICE_F_MOVE)
		if err != nil {
			errChForwardee <- fmt.Errorf("error while splicing from conn to pipe: %s", err)
			return
		}

		nteed := int64(MaxInt)

		for _, m := range mirrorsInt {
			if m.closed {
				continue
			}

			nteed, err = unix.Tee(p[0], m.mirrorPipe[1], MaxInt, SPLICE_F_MOVE)
			if err != nil {
				m.conn.Close()
				m.closed = true
				select {
				case errChMirrors <- fmt.Errorf("error while tee(): %s", err):
				default:
				}
				return
			}
		}

		_, err = unix.Splice(p[0], nullPtr, int(toFile.Fd()), nullPtr, int(nteed), SPLICE_F_MOVE)
		if err != nil {
			errChForwardee <- fmt.Errorf("error while splice(): %s", err)
			return
		}
	}

}

func forwardAndCopy(from net.Conn, to net.Conn, mirrors []mirror, errChForwardee, errChMirrors chan error) {
	for {
		var b [defaultBufferSize]byte

		n, err := from.Read(b[:])
		if err != nil {
			errChForwardee <- err
			return
		}

		_, err = to.Write(b[:n])
		if err != nil {
			errChForwardee <- err
			return
		}

		for i := 0; i < len(mirrors); i++ {
			if mirrors[i].closed {
				continue
			}
			_, err = mirrors[i].conn.Write(b[:n])
			if err != nil {
				mirrors[i].conn.Close()
				mirrors[i].closed = true
				select {
				case errChMirrors <- err:
				default:
				}

			}
		}
	}
}

func connect(origin net.Conn, forwarder net.Conn, mirrors []mirror, useZeroCopy bool, errChForwardee, errChMirrors chan error) {

	for i := 0; i < len(mirrors); i++ {
		go readAndDiscard(mirrors[i], errChMirrors)
	}

	if useZeroCopy {
		go forwardZeroCopy(forwarder, origin, errChForwardee)
		go forwardAndZeroCopy(origin, forwarder, mirrors, errChForwardee, errChMirrors)
	} else {
		go forward(forwarder, origin, errChForwardee)
		go forwardAndCopy(origin, forwarder, mirrors, errChForwardee, errChMirrors)
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

			var mirrors []mirror

			for _, addr := range mirrorAddresses {
				c, err := net.Dial("tcp", addr)
				if err != nil {
					log.Printf("error while connecting to mirror %s: %s", addr, err)
				} else {
					mirrors = append(mirrors, mirror{
						addr:   addr,
						conn:   c,
						closed: false,
					})
				}
			}

			errChForwardee := make(chan error, 2)
			errChMirrors := make(chan error, len(mirrors))

			connect(c, cF, mirrors, useZeroCopy, errChForwardee, errChMirrors)

			done := false
			for !done {
				select {
				case err := <-errChMirrors:
					log.Printf("got error from mirror: %s", err)
				case err := <-errChForwardee:
					log.Printf("got error from forwardee: %s", err)
					done = true
				}
			}

			c.Close()
			cF.Close()

			for _, m := range mirrors {
				m.conn.Close()
			}
		}(c)

		connNo += 1
	}
}
