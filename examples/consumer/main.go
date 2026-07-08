// Command consumer opens a shared-memory ring buffer created by the
// producer example and prints every message it reads until the producer
// closes the ring and it drains.
package main

import (
	"bufio"
	"errors"
	"flag"
	"io"
	"log"
	"time"

	"github.com/gofsd/shmring"
)

func main() {
	name := flag.String("name", "shmring-example", "shared memory segment name")
	capacity := flag.Int64("capacity", 4096, "ring buffer capacity in bytes (must match the producer)")
	flag.Parse()

	r, err := openWithRetry(*name, *capacity, 5*time.Second)
	if err != nil {
		log.Fatalf("OpenShm: %v", err)
	}
	defer r.Close()

	log.Printf("opened shared memory %q, reading until the producer closes it", *name)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		log.Printf("received: %q", scanner.Text())
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		log.Fatalf("read: %v", err)
	}
	log.Print("producer closed and drained, exiting")
}

// openWithRetry retries OpenShm until the producer has created the segment
// or timeout elapses.
func openWithRetry(name string, capacity int64, timeout time.Duration) (*shmring.Reader, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		r, err := shmring.OpenShm(name, capacity)
		if err == nil {
			return r, nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return nil, lastErr
}
