package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

var (
	version = "dev"
	commit  = "unknown"
)

const usageText = `Usage:
  pb [options] [file|-]
  pb get [--password PASSWORD] <code|url>
  pb config validate
  pb version

Upload options:
  --permanent       keep the paste permanently
  --once            delete after the first successful view
  --expires VALUE   expire after a duration such as 30m, 12h, or 7d
  --password        generate password protection
  --code VALUE      use a custom paste code
  --label VALUE     attach a paste label
  --quiet           print only the public URL
  --json            print the JSON response
`

type application struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	httpClient *http.Client
	configPath string
	stdinTTY   bool
}

func main() {
	stdinTTY := false
	if info, err := os.Stdin.Stat(); err == nil {
		stdinTTY = info.Mode()&os.ModeCharDevice != 0
	}

	app := application{
		stdin:      os.Stdin,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		httpClient: http.DefaultClient,
		configPath: defaultConfigPath(),
		stdinTTY:   stdinTTY,
	}
	os.Exit(app.run(os.Args[1:]))
}

func (a application) run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			fmt.Fprint(a.stdout, usageText)
			return 0
		case "version", "--version":
			fmt.Fprintf(a.stdout, "pb %s (commit %s)\n", version, commit)
			return 0
		case "config":
			return a.runConfig(args[1:])
		case "get":
			return a.runGet(args[1:])
		}
	}
	return a.runUpload(args)
}

func (a application) runConfig(args []string) int {
	if len(args) != 1 || args[0] != "validate" {
		fmt.Fprintln(a.stderr, "usage: pb config validate")
		return 2
	}

	cfg, err := loadConfig(a.configPath)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 2
	}
	fmt.Fprintf(a.stdout, "valid config: %s\nserver_url: %s\n", a.configPath, cfg.ServerURL)
	return 0
}

func (a application) runUpload(args []string) int {
	flags := flag.NewFlagSet("upload", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var opts uploadOptions
	flags.BoolVar(&opts.permanent, "permanent", false, "")
	flags.BoolVar(&opts.once, "once", false, "")
	flags.StringVar(&opts.expires, "expires", "", "")
	flags.BoolVar(&opts.usePassword, "password", false, "")
	flags.StringVar(&opts.code, "code", "", "")
	flags.StringVar(&opts.label, "label", "", "")
	flags.BoolVar(&opts.quiet, "quiet", false, "")
	flags.BoolVar(&opts.jsonOutput, "json", false, "")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(a.stderr, "invalid arguments: %v\n", err)
		return 2
	}
	if err := opts.validate(); err != nil {
		fmt.Fprintf(a.stderr, "invalid arguments: %v\n", err)
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(a.stderr, "invalid arguments: provide one file or use stdin")
		return 2
	}
	if len(args) == 0 && a.stdinTTY {
		created, err := initializeConfig(a.configPath)
		if err != nil {
			fmt.Fprintln(a.stderr, err)
			return 2
		}
		if created {
			fmt.Fprintf(a.stdout, "created config: %s\nEdit server_url in this file before using pb.\n", a.configPath)
			return 0
		}
	}

	filename := ""
	reader := a.stdin
	var closeInput io.Closer
	if flags.NArg() == 1 && flags.Arg(0) != "-" {
		file, err := os.Open(flags.Arg(0))
		if err != nil {
			fmt.Fprintf(a.stderr, "cannot open %q: %v\n", flags.Arg(0), err)
			return 2
		}
		reader = file
		closeInput = file
		filename = file.Name()
	} else if a.stdinTTY {
		fmt.Fprintln(a.stderr, "no input: provide a file or pipe text to pb")
		return 2
	}
	if closeInput != nil {
		defer closeInput.Close()
	}

	cfg, err := loadConfig(a.configPath)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 2
	}
	result, raw, err := upload(a.httpClient, cfg, reader, filename, opts)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	if err := writeUploadOutput(a.stdout, result, raw, opts); err != nil {
		fmt.Fprintf(a.stderr, "write output: %v\n", err)
		return 1
	}
	return 0
}

func (a application) runGet(args []string) int {
	flags := flag.NewFlagSet("get", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	password := flags.String("password", "", "")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(a.stderr, "invalid arguments: %v\n", err)
		return 2
	}
	if flags.NArg() != 1 || strings.TrimSpace(flags.Arg(0)) == "" {
		fmt.Fprintln(a.stderr, "usage: pb get [--password PASSWORD] <code|url>")
		return 2
	}

	cfg, err := loadConfig(a.configPath)
	if err != nil {
		fmt.Fprintln(a.stderr, err)
		return 2
	}
	if err := getPaste(a.httpClient, cfg, flags.Arg(0), *password, a.stdout); err != nil {
		fmt.Fprintln(a.stderr, err)
		return 1
	}
	return 0
}
