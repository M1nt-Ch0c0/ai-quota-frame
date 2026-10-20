// Command bake-skin converts generated artwork into a frame skin background:
// 800x480, error-diffused onto the six Spectra 6 inks, with the data slots
// cleared to white.
package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
)

func run() error {
	input := flag.String("input", "", "generated artwork (PNG or JPEG)")
	output := flag.String("output", "", "baked skin PNG")
	flag.Parse()
	if *input == "" || *output == "" {
		return fmt.Errorf("usage: bake-skin -input art.png -output internal/dashboard/templates/skins/<theme>.png")
	}
	file, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer file.Close()
	art, _, err := image.Decode(file)
	if err != nil {
		return fmt.Errorf("decode artwork: %w", err)
	}
	out, err := os.Create(*output)
	if err != nil {
		return err
	}
	if err := png.Encode(out, dashboard.BakeSkin(art)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
