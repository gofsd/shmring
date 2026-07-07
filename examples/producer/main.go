// Command producer creates a shared-memory ring buffer and streams
// numbered messages into it. Run the consumer example against the same
// -name in another terminal (or process) to see them come out the other
// side.
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/madi/shmring"
)

func main() {
	name := flag.String("name", "shmring-example", "shared memory segment name")
	capacity := flag.Int64("capacity", 4096, "ring buffer capacity in bytes (must be a power of two)")
	count := flag.Int("count", 20, "number of messages to send")
	interval := flag.Duration("interval", 200*time.Millisecond, "delay between messages")
	flag.Parse()

	w, err := shmring.CreateShm(*name, *capacity)
	if err != nil {
		log.Fatalf("CreateShm: %v", err)
	}
	// CloseStorage removes the OS shared-memory segment; only the creator
	// (this process) should call it, and only once the consumer is done.
	defer w.CloseStorage()

	log.Printf("created shared memory %q (capacity=%d), producing %d messages", *name, *capacity, *count)

	for i := 0; i < *count; i++ {
		msg := fmt.Sprintf("message %d\n", i)
		if _, err := w.Write([]byte(msg)); err != nil {
			log.Fatalf("Write: %v", err)
		}
		log.Printf("sent: %q", msg[:len(msg)-1])
		time.Sleep(*interval)
	}

	if err := w.Close(); err != nil {
		log.Fatalf("Close: %v", err)
	}
	log.Print("done, waiting a moment for the consumer to drain before removing the segment")
	time.Sleep(2 * time.Second)
}
