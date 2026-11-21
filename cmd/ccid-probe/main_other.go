//go:build !linux

// Command ccid-probe talks to a CCID reader through usbfs, which only
// Linux (and Android) have.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "ccid-probe: usbfs is Linux only")
	os.Exit(1)
}
