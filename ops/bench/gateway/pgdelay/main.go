// pgdelay adds propagation delay to loopback TCP traffic. Buffered packets
// retain arrival deadlines, so a large SQL message does not pay one RTT per
// buffer. It is only a local benchmark fixture, never a production proxy.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

func loopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	return err == nil && net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func main() {
	address := flag.String("addr", "127.0.0.1:15474", "loopback listener")
	target := flag.String("target", "127.0.0.1:15473", "loopback PostgreSQL")
	rtt := flag.Duration("rtt", 35*time.Millisecond, "added round-trip propagation delay")
	flag.Parse()
	if !loopback(*address) || !loopback(*target) || *rtt < 0 || *rtt > time.Second {
		fmt.Fprintln(os.Stderr, "listener/target must be numeric loopback addresses; RTT must be 0..1s")
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer listener.Close()
	for {
		client, err := listener.Accept()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		go tunnel(client, *target, *rtt/2)
	}
}

func tunnel(client net.Conn, target string, delay time.Duration) {
	server, err := net.Dial("tcp", target)
	if err != nil {
		_ = client.Close()
		return
	}
	done := make(chan struct{})
	var once sync.Once
	closeBoth := func() { once.Do(func() { close(done); _ = client.Close(); _ = server.Close() }) }
	var writers sync.WaitGroup
	writers.Add(2)
	for _, pair := range [][2]net.Conn{{client, server}, {server, client}} {
		go func(source, destination net.Conn) {
			defer writers.Done()
			defer closeBoth()
			forward(source, destination, delay, done)
		}(pair[0], pair[1])
	}
	writers.Wait()
}

type packet struct {
	ready time.Time
	body  []byte
}

func forward(source net.Conn, destination net.Conn, delay time.Duration, done <-chan struct{}) {
	packets := make(chan packet, 64) // bounded at 4 MiB per direction
	go func() {
		defer close(packets)
		buffer := make([]byte, 64<<10)
		for {
			n, err := source.Read(buffer)
			if n > 0 {
				p := packet{time.Now().Add(delay), append([]byte(nil), buffer[:n]...)}
				select {
				case packets <- p:
				case <-done:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for p := range packets {
		if wait := time.Until(p.ready); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-done:
				timer.Stop()
				return
			}
		}
		if _, err := io.Copy(destination, bytes.NewReader(p.body)); err != nil {
			return
		}
	}
}
