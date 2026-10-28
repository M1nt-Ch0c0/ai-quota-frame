// Cross-check browser-produced PNGs against the canonical sender validator.
package main

import (
	"fmt"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run web/tools/validate-png.go image.png")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read image")
		os.Exit(1)
	}
	if err = ble.ValidatePNG(data); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Canonical Go PNG validator passed (%d bytes)\n", len(data))
}
