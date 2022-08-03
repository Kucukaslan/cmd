package main

import (
	"context"
	"flag"
	"log"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

func main() {
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, os.Kill)
	defer cancel()

	logger := log.New(os.Stdout, "", 0)

	backend := s3mem.New()
	faker := gofakes3.New(backend)

	ts := httptest.NewServer(faker.Server())
	defer ts.Close()

	logger.Printf("s5cmd --endpoint-url %v", ts.URL)

	<-ctx.Done()
}
