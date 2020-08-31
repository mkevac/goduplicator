package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

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
	closed uint32
}

func readAndDiscard(m mirror, closeCh chan error) {
	for {
		var b [defaultBufferSize]byte
		_, err := m.conn.Read(b[:])
		if err != nil {
			m.conn.Close()
			atomic.StoreUint32(&m.closed, 1)
			select {
			case closeCh <- err:
			default:
			}
			return
		}
	}
}

func forward(from net.Conn, to net.Conn, closeCh chan error) {
	for {
		var b [defaultBufferSize]byte

		n, err := from.Read(b[:])
		if err != nil {
			if err != io.EOF {
				closeCh <- fmt.Errorf("from.Read() failed: %w", err)
			} else {
				closeCh <- nil
			}
			return
		}

		_, err = to.Write(b[:n])
		if err != nil {
			closeCh <- fmt.Errorf("to.Write() failed: %w", err)
			return
		}
	}
}

func forwardZeroCopy(from net.Conn, to net.Conn, closeCh chan error) {
	var (
		p       [2]int
		nullPtr *int64
	)

	if err := unix.Pipe(p[:]); err != nil {
		closeCh <- fmt.Errorf("pipe() error: %w", err)
		return
	}

	fromFile, err := from.(*net.TCPConn).File()
	if err != nil {
		closeCh <- fmt.Errorf("error while creating File() from incoming connection: %w", err)
		return
	}

	toFile, err := to.(*net.TCPConn).File()
	if err != nil {
		closeCh <- fmt.Errorf("error while creating File() from outgoing connection: %w", err)
		return
	}

	for {
		_, err = unix.Splice(int(fromFile.Fd()), nullPtr, p[1], nullPtr, MaxInt, SPLICE_F_MOVE)
		if err != nil {
			closeCh <- fmt.Errorf("error while splicing from conn to pipe: %w", err)
			return
		}
		_, err = unix.Splice(p[0], nullPtr, int(toFile.Fd()), nullPtr, MaxInt, SPLICE_F_MOVE)
		if err != nil {
			closeCh <- fmt.Errorf("error while splicing from pipe to conn: %w", err)
			return
		}
	}
}

func forwardAndZeroCopy(from net.Conn, to net.Conn, mirrors []mirror, closeCh, errorCh chan error) {
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

	if err := unix.Pipe(p[:]); err != nil {
		errorCh <- fmt.Errorf("pipe() error: %w", err)
		return
	}

	fromFile, err := from.(*net.TCPConn).File()
	if err != nil {
		errorCh <- fmt.Errorf("error while creating File() from incoming connection: %w", err)
		return
	}

	toFile, err := to.(*net.TCPConn).File()
	if err != nil {
		errorCh <- fmt.Errorf("error while creating File() from outgoing connection: %w", err)
		return
	}

	for _, m := range mirrors {
		mFile, err := m.conn.(*net.TCPConn).File()
		if err != nil {
			errorCh <- fmt.Errorf("error while creating File() from mirror connection: %w", err)
		}

		var mPipe [2]int

		if err := unix.Pipe(mPipe[:]); err != nil {
			errorCh <- fmt.Errorf("pipe() error: %w", err)
			return
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
					case errorCh <- fmt.Errorf("error while splicing from pipe to conn: %w", err):
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
			closeCh <- fmt.Errorf("error while splicing from conn to pipe: %w", err)
			return
		}

		nteed := int64(MaxInt)

		for _, m := range mirrorsInt {
			if closed := atomic.LoadUint32(&m.closed); closed == 1 {
				continue
			}

			nteed, err = unix.Tee(p[0], m.mirrorPipe[1], MaxInt, SPLICE_F_MOVE)
			if err != nil {
				m.conn.Close()
				atomic.StoreUint32(&m.closed, 1)
				select {
				case errorCh <- fmt.Errorf("error while tee(): %w", err):
				default:
				}
			}
		}

		_, err = unix.Splice(p[0], nullPtr, int(toFile.Fd()), nullPtr, int(nteed), SPLICE_F_MOVE)
		if err != nil {
			closeCh <- fmt.Errorf("error while splice(): %w", err)
			return
		}
	}
}

var writeTimeout time.Duration

func forwardAndCopy(from net.Conn, to net.Conn, mirrors []mirror, closeCh, errorCh chan error) {
	for {
		var b [defaultBufferSize]byte

		n, err := from.Read(b[:])
		if err != nil {
			if err != io.EOF {
				closeCh <- fmt.Errorf("from.Read() failed: %w", err)
			} else {
				closeCh <- nil
			}
			return
		}

		_, err = to.Write(b[:n])
		if err != nil {
			closeCh <- fmt.Errorf("to.Write() failed: %w", err)
			return
		}

		for i := 0; i < len(mirrors); i++ {
			if closed := atomic.LoadUint32(&mirrors[i].closed); closed == 1 {
				continue
			}

			mirrors[i].conn.SetWriteDeadline(time.Now().Add(writeTimeout))

			_, err = mirrors[i].conn.Write(b[:n])
			if err != nil {
				mirrors[i].conn.Close()
				atomic.StoreUint32(&mirrors[i].closed, 1)
				select {
				case errorCh <- err:
				default:
				}

			}
		}
	}
}

func connect(origin net.Conn, forwarder net.Conn, mirrors []mirror, useZeroCopy bool, closeCh chan error, errorCh chan error) {

	for i := 0; i < len(mirrors); i++ {
		go readAndDiscard(mirrors[i], closeCh)
	}

	if useZeroCopy {
		go forwardZeroCopy(forwarder, origin, closeCh)
		go forwardAndZeroCopy(origin, forwarder, mirrors, closeCh, errorCh)
	} else {
		go forward(forwarder, origin, closeCh)
		go forwardAndCopy(origin, forwarder, mirrors, closeCh, errorCh)
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
		connectTimeout  time.Duration
		delay           time.Duration
		listenAddress   string
		forwardAddress  string
		mirrorAddresses mirrorList
		useZeroCopy     bool
	)

	flag.BoolVar(&useZeroCopy, "z", false, "use zero copy")
	flag.StringVar(&listenAddress, "l", "", "listen address (e.g. 'localhost:8080')")
	flag.StringVar(&forwardAddress, "f", "", "forward to address (e.g. 'localhost:8081')")
	flag.Var(&mirrorAddresses, "m", "comma separated list of mirror addresses (e.g. 'localhost:8082,localhost:8083')")
	flag.DurationVar(&connectTimeout, "t", 500*time.Millisecond, "mirror connect timeout")
	flag.DurationVar(&delay, "d", 20*time.Second, "delay connecting to mirror after unsuccessful attempt")
	flag.DurationVar(&writeTimeout, "wt", 20*time.Millisecond, "mirror write timeout")

	flag.Parse()

	if listenAddress == "" || forwardAddress == "" {
		flag.Usage()
		return
	}

	l, err := net.Listen("tcp", listenAddress)
	if err != nil {
		log.Fatalf("error while listening: %s", err)
	}

	var connNo uint64

	for {
		c, err := l.Accept()
		if err != nil {
			log.Printf("Error while accepting: %s", err)
			continue
		}

		log.Printf("accepted connection %d (%s <-> %s)", connNo, c.RemoteAddr(), c.LocalAddr())

		go func(connClient net.Conn) {
			defer connClient.Close()

			connForwardee, err := net.Dial("tcp", forwardAddress)
			if err != nil {
				log.Printf("error while connecting to forwarder (%s), will close client connection", err)
				return
			}
			defer connForwardee.Close()

			var mirrors []mirror

			for _, addr := range mirrorAddresses {
				connMirror, err := net.DialTimeout("tcp", addr, connectTimeout)
				if err != nil {
					log.Printf("error while connecting to mirror %s (%s), will continue", addr, err)
					continue
				}

				mirrors = append(mirrors, mirror{
					addr:   addr,
					conn:   connMirror,
					closed: 0,
				})
			}

			defer func() {
				for i, m := range mirrors {
					if closed := atomic.LoadUint32(&mirrors[i].closed); closed == 1 {
						continue
					}
					m.conn.Close()
				}
			}()

			closeCh := make(chan error, 1024)
			errorCh := make(chan error, 1024)

			connect(connClient, connForwardee, mirrors, useZeroCopy, closeCh, errorCh)

			for {
				select {
				case err := <-errorCh:
					if err != nil {
						log.Printf("got error (%s), will continue", err)
					}
				case err := <-closeCh:
					if err != nil {
						log.Printf("got error (%s), will close client connection", err)
					}
					return
				}
			}
		}(c)

		connNo += 1
	}
}
