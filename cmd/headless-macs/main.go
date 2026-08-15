package main

import (
	"fmt"
	"os"
	"runtime"
)

const version = "2.0.0-dev"

func main() {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "ERROR: macOS required.")
		os.Exit(1)
	}
	if runtime.GOARCH != "arm64" {
		fmt.Fprintln(os.Stderr, "ERROR: Apple Silicon (arm64) required.")
		os.Exit(1)
	}

	fmt.Printf("headless-macs %s\n", version)
	fmt.Println("TUI not yet implemented — Phase 6J pending.")
}
