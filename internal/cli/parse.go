// Package cli parses the command line of ntp-sync.exe. It accepts the new
// commands ("ntp-sync sync", "ntp-sync service install", ...) as well as
// every form version 1.x understood, so existing scripts and installed
// services keep working.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// Command is a parsed command line.
type Command struct {
	Name string // e.g. "sync", "service install", "config set"

	// Banner: started without arguments (the 1.x default); show the
	// overview before synchronizing.
	Banner bool
	// Legacy: started with the flags of version 1.x.
	Legacy bool

	Interval   int
	Servers    []string
	JSON       bool
	Force      bool
	Local      bool
	Out        string
	N          int
	Purge      bool
	KeepBinary bool
	NoPause    bool
	Args       []string
}

// Command names.
const (
	CmdSync             = "sync"
	CmdCheck            = "check"
	CmdStatus           = "status"
	CmdHistory          = "history"
	CmdRun              = "run"
	CmdServiceInstall   = "service install"
	CmdServiceUninstall = "service uninstall"
	CmdServiceStart     = "service start"
	CmdServiceStop      = "service stop"
	CmdServiceRestart   = "service restart"
	CmdServiceRun       = "service run"
	CmdConfigShow       = "config show"
	CmdConfigPath       = "config path"
	CmdConfigSet        = "config set"
	CmdConfigReset      = "config reset"
	CmdConfigImport     = "config import"
	CmdW32TimeStatus    = "w32time status"
	CmdW32TimeRestore   = "w32time restore"
	CmdDiagnose         = "diagnose"
	CmdVersion          = "version"
	CmdHelp             = "help"
)

// ErrUsage marks a command line that could not be understood.
var ErrUsage = errors.New("usage")

// Parse interprets args (without the program name).
func Parse(args []string) (Command, error) {
	if len(args) == 0 {
		return Command{Name: CmdSync, Banner: true}, nil
	}
	first := strings.ToLower(args[0])
	if strings.HasPrefix(first, "-") || strings.HasPrefix(first, "/") {
		return parseLegacy(args)
	}

	var name string
	rest := args[1:]
	switch first {
	case "service", "config", "w32time":
		if len(rest) == 0 {
			return Command{}, fmt.Errorf("%w: %q needs a subcommand", ErrUsage, first)
		}
		name = first + " " + strings.ToLower(rest[0])
		rest = rest[1:]
	default:
		name = first
	}

	c := Command{Name: name}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var servers string
	switch name {
	case CmdSync:
		fs.StringVar(&servers, "server", "", "")
		fs.BoolVar(&c.JSON, "json", false, "")
		fs.BoolVar(&c.Force, "force", false, "")
		fs.BoolVar(&c.Local, "local", false, "")
		fs.StringVar(&c.Out, "out", "", "")
		fs.BoolVar(&c.NoPause, "nopause", false, "")
	case CmdCheck:
		fs.StringVar(&servers, "server", "", "")
		fs.BoolVar(&c.JSON, "json", false, "")
		fs.StringVar(&c.Out, "out", "", "")
		fs.BoolVar(&c.NoPause, "nopause", false, "")
	case CmdStatus:
		fs.BoolVar(&c.JSON, "json", false, "")
		fs.BoolVar(&c.NoPause, "nopause", false, "")
	case CmdHistory:
		fs.IntVar(&c.N, "n", 20, "")
		fs.BoolVar(&c.JSON, "json", false, "")
	case CmdRun:
		fs.IntVar(&c.Interval, "interval", 60, "")
		fs.StringVar(&servers, "server", "", "")
	case CmdServiceInstall:
		fs.IntVar(&c.Interval, "interval", 0, "")
		fs.StringVar(&c.Out, "out", "", "")
		fs.BoolVar(&c.NoPause, "nopause", false, "")
	case CmdServiceUninstall:
		fs.BoolVar(&c.Purge, "purge", false, "")
		fs.BoolVar(&c.KeepBinary, "keep-binary", false, "")
		fs.StringVar(&c.Out, "out", "", "")
		fs.BoolVar(&c.NoPause, "nopause", false, "")
	case CmdServiceRun:
		fs.IntVar(&c.Interval, "interval", 0, "")
	case CmdConfigShow:
		fs.BoolVar(&c.JSON, "json", false, "")
	case CmdConfigImport, CmdConfigSet, CmdConfigReset, CmdW32TimeRestore, CmdServiceStart, CmdServiceStop, CmdServiceRestart:
		fs.StringVar(&c.Out, "out", "", "")
		fs.BoolVar(&c.NoPause, "nopause", false, "")
	case CmdDiagnose:
		fs.StringVar(&c.Out, "out", "", "")
	case CmdConfigPath, CmdW32TimeStatus, CmdVersion, CmdHelp:
	default:
		return Command{}, fmt.Errorf("%w: unknown command %q", ErrUsage, name)
	}
	args, err := parseInterleaved(fs, rest)
	if err != nil {
		return Command{}, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	c.Args = args
	if servers != "" {
		c.Servers = strings.FieldsFunc(servers, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	}
	switch name {
	case CmdConfigSet:
		if len(c.Args) == 0 {
			return Command{}, fmt.Errorf("%w: config set needs key=value", ErrUsage)
		}
		for _, a := range c.Args {
			if !strings.Contains(a, "=") {
				return Command{}, fmt.Errorf("%w: %q is not key=value", ErrUsage, a)
			}
		}
	case CmdConfigImport:
		if len(c.Args) != 1 {
			return Command{}, fmt.Errorf("%w: config import needs exactly one file", ErrUsage)
		}
	default:
		if len(c.Args) > 0 {
			return Command{}, fmt.Errorf("%w: unexpected %q", ErrUsage, c.Args[0])
		}
	}
	if c.Interval < 0 || c.N < 0 {
		return Command{}, fmt.Errorf("%w: numbers must not be negative", ErrUsage)
	}
	return c, nil
}

// parseLegacy understands version 1.x: -interval N, -install N,
// -uninstall, -version and the service's own "-service -interval N".
func parseLegacy(args []string) (Command, error) {
	for _, a := range args {
		switch strings.ToLower(a) {
		case "-h", "--help", "-help", "/?", "/h":
			return Command{Name: CmdHelp}, nil
		}
	}
	fs := flag.NewFlagSet("ntp-sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	service := fs.Bool("service", false, "")
	interval := fs.Int("interval", 0, "")
	install := fs.Int("install", 0, "")
	uninstall := fs.Bool("uninstall", false, "")
	ver := fs.Bool("version", false, "")
	if err := fs.Parse(args); err != nil {
		return Command{}, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	if fs.NArg() > 0 {
		return Command{}, fmt.Errorf("%w: unexpected %q", ErrUsage, fs.Arg(0))
	}
	if *interval < 0 || *install < 0 {
		return Command{}, fmt.Errorf("%w: the interval must be at least 1 minute", ErrUsage)
	}
	c := Command{Legacy: true}
	switch {
	case *ver:
		c.Name = CmdVersion
	case *service:
		c.Name, c.Interval = CmdServiceRun, *interval
	case *uninstall:
		c.Name = CmdServiceUninstall
	case *install > 0:
		c.Name, c.Interval = CmdServiceInstall, *install
	case *interval > 0:
		c.Name, c.Interval = CmdRun, *interval
	default:
		c.Name = CmdSync
	}
	return c, nil
}

// parseInterleaved lets flags follow positional arguments
// ("config import file.json -out result.json"), which the flag package
// alone does not allow.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
