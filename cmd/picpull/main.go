// Command picpull downloads the images on a web page in on-screen order.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/wonhajin/picpull/internal/app"
	"github.com/wonhajin/picpull/internal/browser"
	"github.com/wonhajin/picpull/internal/config"
	"github.com/wonhajin/picpull/internal/output"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	home, _ := os.UserHomeDir()
	o, err := config.Parse(args, config.Env{Home: home, Getenv: os.Getenv, ReadFile: os.ReadFile})
	if errors.Is(err, config.ErrHelp) {
		fmt.Fprint(stdout, config.Usage())
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\nRun 'picpull --help' for usage.\n", err)
		return 2
	}
	if o.ShowVersion {
		fmt.Fprintf(stdout, "picpull %s\n", version)
		return 0
	}

	tty, width := false, 0
	if f, ok := stdout.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		tty = true
		width, _, _ = term.GetSize(int(f.Fd()))
	}
	color := tty && os.Getenv("NO_COLOR") == ""

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, o, app.Deps{
		Printer:     output.New(stdout, stderr, o.Level, tty, color, width),
		FindBrowser: browser.Find,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
		Sleep:       app.SleepContext,
	})
}
