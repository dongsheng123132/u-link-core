//go:build windows

package desktop

import (
	"os"
	"os/signal"
)

func notifyTermination(ch chan<- os.Signal) {
	signal.Notify(ch, os.Interrupt)
}
