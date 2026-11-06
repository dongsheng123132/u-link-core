//go:build !windows

package desktop

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyTermination(ch chan<- os.Signal) {
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
}
