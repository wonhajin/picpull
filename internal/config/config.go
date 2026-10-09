// Package config merges command-line flags over the config file over
// built-in defaults.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/pflag"

	"github.com/wonhj12/picpull/internal/output"
)

var ErrHelp = pflag.ErrHelp

type Options struct {
	URL         string
	Out         string // final folder; empty means DownloadDir/<page title>
	DownloadDir string
	Next        string
	MaxPages    int // 0 means no limit
	Delay       time.Duration
	MinSize     int
	In          string
	Match       *regexp.Regexp
	AllNetwork  bool
	KeepNames   bool
	Level       output.Level
	ShowVersion bool
}

type Env struct {
	Home     string
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)
}

// fileConfig holds only global defaults. Site-specific options (next, in,
// match, out) are left out on purpose: one value for every site is wrong.
type fileConfig struct {
	DownloadDir *string  `toml:"download-dir"`
	Delay       *float64 `toml:"delay"`
	MaxPages    *int     `toml:"max-pages"`
	MinSize     *int     `toml:"min-size"`
	KeepNames   *bool    `toml:"keep-names"`
	AllNetwork  *bool    `toml:"all-network"`
}

type flagValues struct {
	out, next, in, match  string
	maxPages, minSize     int
	delay                 float64
	allNetwork, keepNames bool
	verbose, quiet        bool
}

func newFlagSet(v *flagValues) *pflag.FlagSet {
	fs := pflag.NewFlagSet("picpull", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.SortFlags = true
	fs.StringVarP(&v.out, "out", "o", "", "save into this folder (default: <download-dir>/<page title>)")
	fs.StringVar(&v.next, "next", "", "follow the next-page button matched by CSS selector or exact text")
	fs.IntVar(&v.maxPages, "max-pages", 0, "stop after this many pages (0 = no limit)")
	fs.Float64Var(&v.delay, "delay", 2, "seconds to wait between pages")
	fs.IntVar(&v.minSize, "min-size", 0, "skip images narrower or shorter than this many pixels")
	fs.StringVar(&v.in, "in", "", "only images inside the area matched by this CSS selector")
	fs.StringVar(&v.match, "match", "", "only images whose URL matches this regular expression")
	fs.BoolVar(&v.allNetwork, "all-network", false, "also save every image the page loaded over the network")
	fs.BoolVar(&v.keepNames, "keep-names", false, "keep original file names instead of 001, 002, ...")
	fs.BoolVarP(&v.verbose, "verbose", "v", false, "show every image")
	fs.BoolVarP(&v.quiet, "quiet", "q", false, "show only the final summary")
	fs.BoolP("help", "h", false, "show this help")
	return fs
}

func Usage() string {
	var v flagValues
	return "Usage: picpull <url> [flags]\n" +
		"       picpull version\n\n" +
		"Download the images on a web page in the order they appear on screen.\n\n" +
		"Flags:\n" + newFlagSet(&v).FlagUsages()
}

func Parse(args []string, env Env) (Options, error) {
	o := Options{
		DownloadDir: filepath.Join(env.Home, "Downloads"),
		Delay:       2 * time.Second,
		Level:       output.Normal,
	}

	var v flagValues
	fs := newFlagSet(&v)
	if err := fs.Parse(args); err != nil {
		return o, err
	}

	// Check for --help first, before reading config file
	if h, _ := fs.GetBool("help"); h {
		return o, ErrHelp
	}

	// Check for version command, before reading config file
	rest := fs.Args()
	if len(rest) == 1 && rest[0] == "version" {
		o.ShowVersion = true
		return o, nil
	}

	// Now read the config file
	if err := applyFile(&o, env); err != nil {
		return o, err
	}

	// Validate URL
	switch {
	case len(rest) == 0:
		return o, errors.New("missing URL")
	case len(rest) > 1:
		return o, fmt.Errorf("expected one URL, got %d", len(rest))
	case !strings.HasPrefix(rest[0], "http://") && !strings.HasPrefix(rest[0], "https://"):
		return o, errors.New("URL must start with http:// or https://")
	}
	o.URL = rest[0]

	// Apply flag values
	if v.verbose && v.quiet {
		return o, errors.New("-v and -q cannot be used together")
	}
	if v.verbose {
		o.Level = output.Verbose
	}
	if v.quiet {
		o.Level = output.Quiet
	}
	if fs.Changed("out") {
		o.Out = expandHome(v.out, env.Home)
	}
	if fs.Changed("delay") {
		o.Delay = time.Duration(v.delay * float64(time.Second))
	}
	if fs.Changed("max-pages") {
		o.MaxPages = v.maxPages
	}
	if fs.Changed("min-size") {
		o.MinSize = v.minSize
	}
	if fs.Changed("all-network") {
		o.AllNetwork = v.allNetwork
	}
	if fs.Changed("keep-names") {
		o.KeepNames = v.keepNames
	}
	o.Next, o.In = v.next, v.in
	if v.match != "" {
		re, err := regexp.Compile(v.match)
		if err != nil {
			return o, fmt.Errorf("invalid --match regex: %v", err)
		}
		o.Match = re
	}
	return o, validate(o)
}

func validate(o Options) error {
	switch {
	case o.Delay < 0:
		return errors.New("--delay must not be negative")
	case o.MaxPages < 0:
		return errors.New("--max-pages must not be negative")
	case o.MinSize < 0:
		return errors.New("--min-size must not be negative")
	}
	return nil
}

func configPath(env Env) string {
	if dir := env.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "picpull", "config.toml")
	}
	return filepath.Join(env.Home, ".config", "picpull", "config.toml")
}

func applyFile(o *Options, env Env) error {
	path := configPath(env)
	data, err := env.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config file %s: %v", path, err)
	}
	var fc fileConfig
	md, err := toml.Decode(string(data), &fc)
	if err != nil {
		return fmt.Errorf("config file %s: %v", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return fmt.Errorf("config file %s: unknown key %q", path, keys[0].String())
	}
	if fc.DownloadDir != nil {
		o.DownloadDir = expandHome(*fc.DownloadDir, env.Home)
	}
	if fc.Delay != nil {
		o.Delay = time.Duration(*fc.Delay * float64(time.Second))
	}
	if fc.MaxPages != nil {
		o.MaxPages = *fc.MaxPages
	}
	if fc.MinSize != nil {
		o.MinSize = *fc.MinSize
	}
	if fc.KeepNames != nil {
		o.KeepNames = *fc.KeepNames
	}
	if fc.AllNetwork != nil {
		o.AllNetwork = *fc.AllNetwork
	}
	return nil
}

// expandHome handles "~/" because the shell does not expand it inside a
// config file or a quoted flag value.
func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
