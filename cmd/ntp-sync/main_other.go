//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("ntp-sync runs on Windows only.")
	os.Exit(1)
}
