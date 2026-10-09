// Command picpull downloads the images on a web page in on-screen order.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintf(stdout, "picpull %s\n", version)
		return 0
	}
	fmt.Fprintln(stderr, "Usage: picpull <url> [flags]")
	return 2
}
