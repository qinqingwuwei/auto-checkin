package main

import (
	"checkin/internal/cli"
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Main(ctx, os.Args[1:])
	cancel()
	os.Exit(code)
}
