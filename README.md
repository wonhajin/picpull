# picpull

Download the images on a web page in the order they appear on screen.

picpull opens the page in Google Chrome, scrolls through it so lazy-loaded images appear, and saves them as `001.jpg`, `002.png`, … in on-screen order. It can also follow a "next" button across pages.

## Requirements

- macOS or Linux
- Google Chrome
  - macOS: `/Applications/Google Chrome.app` or `~/Applications/Google Chrome.app`
  - Linux: `google-chrome` or `google-chrome-stable` on `PATH`

## Install

```sh
brew install wonhj12/tap/picpull
```

Or with Go:

```sh
go install github.com/wonhj12/picpull/cmd/picpull@latest
```

## Usage

```sh
picpull https://example.com/gallery
```

Images are saved to `~/Downloads/<page title>/`.

Follow a next-page button by CSS selector or by its exact visible text:

```sh
picpull https://example.com/chapter/1 --next "a.next"
picpull https://example.com/chapter/1 --next "Next" --max-pages 10
```

Only keep the large images inside the main content:

```sh
picpull https://example.com/post --in "article" --min-size 300
```

### Flags

```
      --all-network     also save every image the page loaded over the network
      --delay float     seconds to wait between pages (default 2)
  -h, --help            show this help
      --in string       only images inside the area matched by this CSS selector
      --keep-names      keep original file names instead of 001, 002, ...
      --match string    only images whose URL matches this regular expression
      --max-pages int   stop after this many pages (0 = no limit)
      --min-size int    skip images narrower or shorter than this many pixels
      --next string     follow the next-page button matched by CSS selector or exact text
  -o, --out string      save into this folder (default: <download-dir>/<page title>)
  -q, --quiet           show only the final summary
  -v, --verbose         show every image
```

When several filters are given, an image must pass all of them.

### Where files go

```
single page:  <download-dir>/<page title>/001.jpg
with --next:  <download-dir>/<page title>/001/001.jpg, 002/001.jpg, ...
with --out:   DIR/001.jpg  or  DIR/001/001.jpg, ...
```

Files that already exist are skipped, so running the same command again resumes an interrupted download.

Paging stops when the next button is not found, clicking it does not change the address, it leads back to a page already visited, or `--max-pages` is reached.

## Configuration

Defaults can be set in `$XDG_CONFIG_HOME/picpull/config.toml`, or `~/.config/picpull/config.toml` if `XDG_CONFIG_HOME` is not set. Command-line flags override it.

```toml
download-dir = "~/Pictures"   # default ~/Downloads
delay = 1.5
min-size = 200
```

Allowed keys: `download-dir`, `delay`, `max-pages`, `min-size`, `keep-names`, `all-network`. Site-specific options (`--next`, `--in`, `--match`, `--out`) are command-line only.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | all images saved |
| 1 | some images failed, the page could not be loaded, or interrupted |
| 2 | invalid flags |

## License

[MIT](LICENSE)
