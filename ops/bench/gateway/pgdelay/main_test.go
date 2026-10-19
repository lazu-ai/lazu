package main

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestForwardPropagationDelayPreservesLargeBurst(t *testing.T) {
	source, reader := net.Pipe()
	writer, destination := net.Pipe()
	t.Cleanup(func() { _ = source.Close(); _ = reader.Close(); _ = writer.Close(); _ = destination.Close() })
	done := make(chan struct{})
	defer close(done)
	body := bytes.Repeat([]byte("different-packet-data"), (3<<20)/21)
	started := time.Now()
	go forward(reader, writer, 40*time.Millisecond, done)
	go func() { _, _ = source.Write(body); _ = source.Close() }()
	_ = destination.SetReadDeadline(time.Now().Add(time.Second))
	actual := make([]byte, len(body))
	if _, err := io.ReadFull(destination, actual); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, actual) {
		t.Fatal("proxy changed byte order or content")
	}
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("burst paid propagation latency per packet: %v", elapsed)
	}
}

func TestProxyRequiresLoopback(t *testing.T) {
	for address, expected := range map[string]bool{"127.0.0.1:5432": true, "[::1]:5432": true, "0.0.0.0:5432": false, "example.com:5432": false, "192.0.2.1:5432": false} {
		if loopback(address) != expected {
			t.Fatalf("loopback(%q)", address)
		}
	}
}
