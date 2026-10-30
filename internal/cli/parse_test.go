package cli

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		args []string
		want Command
	}{
		{nil, Command{Name: CmdSync, Banner: true}},
		// Version 1.x forms, including the installed service's command line.
		{[]string{"-version"}, Command{Name: CmdVersion, Legacy: true}},
		{[]string{"-interval", "30"}, Command{Name: CmdRun, Legacy: true, Interval: 30}},
		{[]string{"-install", "60"}, Command{Name: CmdServiceInstall, Legacy: true, Interval: 60}},
		{[]string{"-uninstall"}, Command{Name: CmdServiceUninstall, Legacy: true}},
		{[]string{"-service", "-interval", "30"}, Command{Name: CmdServiceRun, Legacy: true, Interval: 30}},
		{[]string{"/?"}, Command{Name: CmdHelp}},
		// New forms.
		{[]string{"check", "-json"}, Command{Name: CmdCheck, JSON: true}},
		{[]string{"sync", "--server", "ptbtime1.ptb.de,ptbtime2.ptb.de", "-force"},
			Command{Name: CmdSync, Servers: []string{"ptbtime1.ptb.de", "ptbtime2.ptb.de"}, Force: true}},
		{[]string{"Service", "Install", "-interval", "15"}, Command{Name: CmdServiceInstall, Interval: 15}},
		{[]string{"service", "uninstall", "-purge"}, Command{Name: CmdServiceUninstall, Purge: true}},
		{[]string{"service", "run"}, Command{Name: CmdServiceRun}},
		{[]string{"config", "set", "interval=30", "eventLog=false"}, Command{Name: CmdConfigSet, Args: []string{"interval=30", "eventLog=false"}}},
		{[]string{"config", "import", `C:\Temp\x.json`, "-out", "r.json"}, Command{Name: CmdConfigImport, Args: []string{`C:\Temp\x.json`}, Out: "r.json"}},
		{[]string{"history", "-n", "5"}, Command{Name: CmdHistory, N: 5}},
		{[]string{"diagnose", "-out", "d.txt"}, Command{Name: CmdDiagnose, Out: "d.txt"}},
	}
	for _, c := range cases {
		got, err := Parse(c.args)
		if err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if got.Name == CmdHistory && c.want.N == 0 {
			c.want.N = 20
		}
		if got.Name == CmdRun && c.want.Interval == 0 {
			c.want.Interval = 60
		}
		if !equal(got, c.want) {
			t.Errorf("%v:\n got %+v\nwant %+v", c.args, got, c.want)
		}
	}
}

func equal(a, b Command) bool {
	if len(a.Servers) != len(b.Servers) || len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Servers {
		if a.Servers[i] != b.Servers[i] {
			return false
		}
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	a.Servers, b.Servers, a.Args, b.Args = nil, nil, nil, nil
	return a.Name == b.Name && a.Banner == b.Banner && a.Legacy == b.Legacy && a.Interval == b.Interval &&
		a.JSON == b.JSON && a.Force == b.Force && a.Out == b.Out && a.N == b.N && a.Purge == b.Purge
}

func TestParseErrors(t *testing.T) {
	bad := [][]string{
		{"-install", "-5"},
		{"-interval", "x"},
		{"-bogus"},
		{"frobnicate"},
		{"service"},
		{"service", "explode"},
		{"config", "set"},
		{"config", "set", "novalue"},
		{"config", "import"},
		{"sync", "extra"},
		{"history", "-n", "-1"},
	}
	for _, args := range bad {
		if _, err := Parse(args); !errors.Is(err, ErrUsage) {
			t.Errorf("%v: want a usage error, got %v", args, err)
		}
	}
}
