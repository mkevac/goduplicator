package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	serverAddress string
	parallel      uint
	requests      uint64
	messageSize   uint
	wg            sync.WaitGroup
)

func client(ctx context.Context) {
	defer wg.Done()

	c, err := net.Dial("tcp", serverAddress)
	if err != nil {
		log.Fatalf("error while connecting to server: %s", err)
	}

	var done uint64

	go func() {
		<-ctx.Done()
		atomic.AddUint64(&done, 1)

	}()

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
		if atomic.LoadUint64(&done) > 0 {
			return
		}
	}
}

func main() {
	flag.UintVar(&messageSize, "s", 1024, "message size in bytes")
	flag.StringVar(&serverAddress, "a", ":11000", "server address to connect to")
	flag.UintVar(&parallel, "p", 10, "how many parallel connections")
	flag.Parse()

	signalChan := make(chan os.Signal, 1024)

	signal.Notify(signalChan, syscall.SIGINT)

	ctx, cancel := context.WithCancel(context.Background())

	for i := uint(0); i < parallel; i++ {
		wg.Add(1)
		go client(ctx)
	}

	oldValue := atomic.LoadUint64(&requests)
	ticker := time.Tick(time.Second)

	for {
		select {
		case <-ticker:
			newValue := atomic.LoadUint64(&requests)
			log.Printf("%v req/sec", newValue-oldValue)
			oldValue = newValue
		case s := <-signalChan:
			log.Printf("received signal %v, waiting for all clients to exit", s)
			cancel()
			wg.Wait()
			log.Printf("exiting...")
			return
		}
	}
}
