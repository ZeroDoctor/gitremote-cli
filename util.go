package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// OnExitWithContext calls function before exiting program and if context cancel, will stop listening for on exit events
func OnExitWithContext(ctx context.Context, fn func(os.Signal, ...interface{}), args ...interface{}) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGQUIT, syscall.SIGINT, syscall.SIGILL, syscall.SIGTERM)

	var sig os.Signal
	select {
	case <-ctx.Done():
		return
	case sig = <-sigChan:
	}

	fmt.Println(" - cleanup request started with", sig, "signal")
	fn(sig, args...)
}
