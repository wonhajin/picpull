package config

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wonhj12/picpull/internal/output"
)

func env(files map[string]string, vars map[string]string) Env {
	return Env{
		Home:   "/home/u",
		Getenv: func(k string) string { return vars[k] },
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, fs.ErrNotExist
		},
	}
}

func TestDefaults(t *testing.T) {
	o, err := Parse([]string{"https://example.com/a"}, env(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if o.URL != "https://example.com/a" || o.DownloadDir != "/home/u/Downloads" ||
		o.Delay != 2*time.Second || o.Level != output.Normal || o.Out != "" ||
		o.MaxPages != 0 || o.MinSize != 0 || o.Match != nil {
		t.Fatalf("defaults = %+v", o)
	}
}

func TestFlagsAfterURL(t *testing.T) {
	o, err := Parse([]string{"https://e.com", "-v", "-o", "~/pics", "--next", "다음 >",
		"--max-pages", "5", "--delay", "0.5", "--min-size", "200", "--in", "#c",
		"--match", `\d+\.jpg$`, "--all-network", "--keep-names"}, env(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if o.Level != output.Verbose || o.Out != "/home/u/pics" || o.Next != "다음 >" ||
		o.MaxPages != 5 || o.Delay != 500*time.Millisecond || o.MinSize != 200 ||
		o.In != "#c" || !o.Match.MatchString("x/12.jpg") || !o.AllNetwork || !o.KeepNames {
		t.Fatalf("options = %+v", o)
	}
}

func TestConfigFileThenFlagsOverride(t *testing.T) {
	files := map[string]string{"/home/u/.config/picpull/config.toml": `
download-dir = "~/Pictures"
delay = 1
min-size = 100
keep-names = true
`}
	o, err := Parse([]string{"https://e.com", "--min-size", "300"}, env(files, nil))
	if err != nil {
		t.Fatal(err)
	}
	if o.DownloadDir != "/home/u/Pictures" || o.Delay != time.Second ||
		o.MinSize != 300 || !o.KeepNames {
		t.Fatalf("options = %+v", o)
	}
}

func TestXDGConfigHome(t *testing.T) {
	files := map[string]string{"/xdg/picpull/config.toml": `delay = 3`}
	o, err := Parse([]string{"https://e.com"}, env(files, map[string]string{"XDG_CONFIG_HOME": "/xdg"}))
	if err != nil {
		t.Fatal(err)
	}
	if o.Delay != 3*time.Second {
		t.Fatalf("delay = %v", o.Delay)
	}
}

func TestVersionAndHelp(t *testing.T) {
	o, err := Parse([]string{"version"}, env(nil, nil))
	if err != nil || !o.ShowVersion {
		t.Fatalf("version: %+v %v", o, err)
	}
	if _, err := Parse([]string{"--help"}, env(nil, nil)); !errors.Is(err, ErrHelp) {
		t.Fatalf("help err = %v", err)
	}
	if !strings.Contains(Usage(), "Usage: picpull <url> [flags]") || !strings.Contains(Usage(), "--next string") {
		t.Fatalf("usage = %q", Usage())
	}
}

func TestHelpWithBrokenConfig(t *testing.T) {
	files := map[string]string{"/home/u/.config/picpull/config.toml": `delay = `}
	o, err := Parse([]string{"--help"}, env(files, nil))
	if !errors.Is(err, ErrHelp) {
		t.Fatalf("--help with broken config: err = %v, want ErrHelp", err)
	}
	if o.ShowVersion {
		t.Fatalf("--help should not set ShowVersion, got %+v", o)
	}
}

func TestVersionWithBrokenConfig(t *testing.T) {
	files := map[string]string{"/home/u/.config/picpull/config.toml": `delay = `}
	o, err := Parse([]string{"version"}, env(files, nil))
	if err != nil {
		t.Fatalf("version with broken config: err = %v, want nil", err)
	}
	if !o.ShowVersion {
		t.Fatalf("version should set ShowVersion, got %+v", o)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		args  []string
		files map[string]string
		want  string
	}{
		{[]string{}, nil, "missing URL"},
		{[]string{"https://a", "https://b"}, nil, "expected one URL, got 2"},
		{[]string{"example.com"}, nil, "URL must start with http:// or https://"},
		{[]string{"https://a", "-v", "-q"}, nil, "-v and -q cannot be used together"},
		{[]string{"https://a", "--match", "("}, nil, "invalid --match regex: "},
		{[]string{"https://a", "--delay", "-1"}, nil, "--delay must be between 0 and 3600"},
		{[]string{"https://a", "--delay", "3601"}, nil, "--delay must be between 0 and 3600"},
		{[]string{"https://a", "--delay", "1e300"}, nil, "--delay must be between 0 and 3600"},
		{[]string{"https://a", "--delay", "NaN"}, nil, "--delay must be between 0 and 3600"},
		{[]string{"https://a"}, map[string]string{"/home/u/.config/picpull/config.toml": `delay = 1e300`},
			"--delay must be between 0 and 3600"},
		{[]string{"https://a", "--max-pages", "-1"}, nil, "--max-pages must not be negative"},
		{[]string{"https://a", "--min-size", "-1"}, nil, "--min-size must not be negative"},
		{[]string{"https://a"}, map[string]string{"/home/u/.config/picpull/config.toml": `next = "x"`},
			`config file /home/u/.config/picpull/config.toml: unknown key "next"`},
		{[]string{"https://a"}, map[string]string{"/home/u/.config/picpull/config.toml": `delay = `},
			"config file /home/u/.config/picpull/config.toml: "},
	}
	for _, c := range cases {
		_, err := Parse(c.args, env(c.files, nil))
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("Parse(%q) err = %v, want prefix %q", c.args, err, c.want)
		}
	}
}

func TestURLSchemeIsCaseInsensitive(t *testing.T) {
	for _, u := range []string{"HTTP://example.com", "Https://example.com"} {
		o, err := Parse([]string{u}, env(nil, nil))
		if err != nil || o.URL != u {
			t.Fatalf("Parse(%q) = %q, %v", u, o.URL, err)
		}
	}
}

func TestUsageGolden(t *testing.T) {
	want, err := os.ReadFile("testdata/usage.golden")
	if err != nil {
		t.Fatal(err)
	}
	if got := Usage(); got != string(want) {
		t.Fatalf("Usage() =\n%s\nwant\n%s", got, want)
	}
}

func TestConfigReadErrorOtherThanNotExist(t *testing.T) {
	e := env(nil, nil)
	e.ReadFile = func(string) ([]byte, error) { return nil, fs.ErrPermission }
	_, err := Parse([]string{"https://e.com"}, e)
	want := "config file /home/u/.config/picpull/config.toml: "
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want prefix %q", err, want)
	}
}

func TestBoolFlagOverridesConfigFile(t *testing.T) {
	files := map[string]string{"/home/u/.config/picpull/config.toml": "keep-names = true\nall-network = true"}
	o, err := Parse([]string{"https://e.com", "--keep-names=false", "--all-network=false"}, env(files, nil))
	if err != nil {
		t.Fatal(err)
	}
	if o.KeepNames || o.AllNetwork {
		t.Fatalf("flags did not override config: %+v", o)
	}
}
