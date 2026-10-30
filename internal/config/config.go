// Package config describes the settings shared by the service, the command
// line tool and the window. The file lives in ProgramData and only
// administrators may change it: whoever chooses the time servers effectively
// chooses the system time, which standard users are not allowed to set.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// W32Time modes.
const (
	// W32TimeTakeover stops the Windows Time service from setting the clock
	// (its original configuration is backed up and restored on uninstall).
	W32TimeTakeover = "takeover"
	// W32TimeLeave leaves the Windows Time service untouched.
	W32TimeLeave = "leave"
)

// Limits for user-provided values.
const (
	// At most four times an hour: the NTP Pool's guidance for clients
	// ("once or twice a day to 4-5 times an hour"), and plenty for a PC.
	MinIntervalMinutes = 15
	MaxIntervalMinutes = 1440
	MaxServers         = 10
	MaxSlewMs          = 5000
	MinBigJumpMinutes  = 1
	MaxBigJumpMinutes  = 24 * 60
)

// DefaultServers come from three operators that offer their time service
// to the public: Cloudflare, the PTB (Germany's national metrology
// institute, which provides the legal time) and Microsoft. Three operators
// mean that one of them going wrong is outvoted or at least noticed.
//
// The NTP Pool asks distributed software not to use pool.ntp.org itself as
// a default; users may of course enter it, and the window offers it as a
// preset. Once the pool has granted TimeSync a vendor zone, its names
// ({0,1,2,3}.<zone>.pool.ntp.org) can become the default here.
var DefaultServers = []string{
	"time.cloudflare.com",
	"ptbtime1.ptb.de",
	"ptbtime2.ptb.de",
	"time.windows.com",
}

// DefaultHTTPSFallbackURLs are asked for their "Date" header only when no NTP
// server answers at all, which typically means UDP port 123 is blocked.
var DefaultHTTPSFallbackURLs = []string{
	"https://www.ptb.de/",
	"https://www.cloudflare.com/",
	"https://www.google.com/",
	"https://www.microsoft.com/",
}

// Config is the content of config.json.
type Config struct {
	// Servers are host names or addresses, optionally with ":port".
	// A "nts://" prefix selects Network Time Security for that server.
	Servers []string `json:"servers"`

	// IntervalMinutes is the time between regular synchronizations.
	IntervalMinutes int `json:"intervalMinutes"`

	SyncOnStart         bool `json:"syncOnStart"`
	SyncOnResume        bool `json:"syncOnResume"`
	SyncOnNetworkChange bool `json:"syncOnNetworkChange"`

	// MinCorrectionMs: smaller deviations are left alone.
	MinCorrectionMs int `json:"minCorrectionMs"`

	// SlewMaxMs: deviations up to this size are corrected gently by running
	// the clock slightly faster or slower. 0 always steps.
	SlewMaxMs int `json:"slewMaxMs"`

	// BigJumpMinutes: corrections larger than this need at least three
	// agreeing servers (or all servers, if fewer are configured).
	BigJumpMinutes int `json:"bigJumpMinutes"`

	// HTTPSFallback asks web servers for the time when no NTP server
	// answers. Precision is about one second.
	HTTPSFallback     bool     `json:"httpsFallback"`
	HTTPSFallbackURLs []string `json:"httpsFallbackUrls"`

	// W32TimeMode is W32TimeTakeover or W32TimeLeave.
	W32TimeMode string `json:"w32timeMode"`

	// AllowOnDomain lets TimeSync set the clock on a computer that is a
	// member of a Windows domain. Off by default: there the domain
	// controller is the time authority, and Kerberos sign-in depends on it.
	AllowOnDomain bool `json:"allowOnDomain"`

	// EventLog writes important events to the Windows event log.
	EventLog bool `json:"eventLog"`
}

// Default returns the settings used when no file exists.
func Default() Config {
	return Config{
		Servers:             append([]string(nil), DefaultServers...),
		IntervalMinutes:     60,
		SyncOnStart:         true,
		SyncOnResume:        true,
		SyncOnNetworkChange: true,
		MinCorrectionMs:     2,
		SlewMaxMs:           500,
		BigJumpMinutes:      15,
		HTTPSFallback:       true,
		HTTPSFallbackURLs:   append([]string(nil), DefaultHTTPSFallbackURLs...),
		W32TimeMode:         W32TimeTakeover,
		AllowOnDomain:       false,
		EventLog:            true,
	}
}

// Normalize trims whitespace, drops empty and duplicate servers and fills in
// missing values with defaults. It does not reject anything; see Validate.
func (c *Config) Normalize() {
	c.Servers = cleanList(c.Servers, true)
	c.HTTPSFallbackURLs = cleanList(c.HTTPSFallbackURLs, false)
	if len(c.Servers) == 0 {
		c.Servers = append([]string(nil), DefaultServers...)
	}
	if c.HTTPSFallback && len(c.HTTPSFallbackURLs) == 0 {
		c.HTTPSFallbackURLs = append([]string(nil), DefaultHTTPSFallbackURLs...)
	}
	if c.W32TimeMode == "" {
		c.W32TimeMode = W32TimeTakeover
	}
}

func cleanList(in []string, lower bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if lower {
			s = strings.ToLower(s)
		}
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Validate reports the first problem found, in words a user can act on.
func (c Config) Validate() error {
	if len(c.Servers) == 0 {
		return errors.New("at least one time server is required")
	}
	if len(c.Servers) > MaxServers {
		return fmt.Errorf("at most %d time servers are allowed", MaxServers)
	}
	for _, s := range c.Servers {
		if err := ValidateServer(s); err != nil {
			return err
		}
	}
	if c.IntervalMinutes < MinIntervalMinutes || c.IntervalMinutes > MaxIntervalMinutes {
		return fmt.Errorf("the interval must be between %d and %d minutes", MinIntervalMinutes, MaxIntervalMinutes)
	}
	if c.MinCorrectionMs < 0 || c.MinCorrectionMs > 1000 {
		return errors.New("minCorrectionMs must be between 0 and 1000")
	}
	if c.SlewMaxMs < 0 || c.SlewMaxMs > MaxSlewMs {
		return fmt.Errorf("slewMaxMs must be between 0 and %d", MaxSlewMs)
	}
	if c.BigJumpMinutes < MinBigJumpMinutes || c.BigJumpMinutes > MaxBigJumpMinutes {
		return fmt.Errorf("bigJumpMinutes must be between %d and %d", MinBigJumpMinutes, MaxBigJumpMinutes)
	}
	for _, u := range c.HTTPSFallbackURLs {
		if !strings.HasPrefix(u, "https://") || strings.ContainsAny(u, " \t\r\n") || len(u) > 300 {
			return fmt.Errorf("invalid fallback address %q (must start with https://)", u)
		}
	}
	if c.W32TimeMode != W32TimeTakeover && c.W32TimeMode != W32TimeLeave {
		return fmt.Errorf("w32timeMode must be %q or %q", W32TimeTakeover, W32TimeLeave)
	}
	return nil
}

// NTSPrefix marks a server that is queried with Network Time Security.
const NTSPrefix = "nts://"

// SplitServer separates the NTS marker from the address.
func SplitServer(s string) (address string, nts bool) {
	if strings.HasPrefix(strings.ToLower(s), NTSPrefix) {
		return s[len(NTSPrefix):], true
	}
	return s, false
}

// ValidateServer checks one server entry: a host name, IPv4 or IPv6
// address, optionally with a port, optionally prefixed with nts://.
func ValidateServer(s string) error {
	addr, _ := SplitServer(s)
	if addr == "" || len(addr) > 260 || strings.ContainsAny(addr, " \t\r\n/\\?#@") {
		return fmt.Errorf("invalid time server %q", s)
	}
	host := addr
	if h, p, err := net.SplitHostPort(addr); err == nil {
		port, perr := strconv.Atoi(p)
		if perr != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid port in time server %q", s)
		}
		host = h
	} else if strings.Count(addr, ":") == 1 {
		return fmt.Errorf("invalid time server %q", s)
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if !isHostName(host) {
		return fmt.Errorf("invalid time server %q", s)
	}
	return nil
}

func isHostName(h string) bool {
	if len(h) == 0 || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// Load reads the file. A missing file yields the defaults and no error.
// Values missing from the file keep their defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), fmt.Errorf("%s is damaged (%v), using defaults", path, err)
	}
	cfg.Normalize()
	// Older files may still ask for a shorter interval than allowed today.
	if cfg.IntervalMinutes > 0 && cfg.IntervalMinutes < MinIntervalMinutes {
		cfg.IntervalMinutes = MinIntervalMinutes
	}
	if err := cfg.Validate(); err != nil {
		return Default(), fmt.Errorf("%s: %v, using defaults", path, err)
	}
	return cfg, nil
}

// Parse decodes and validates settings handed over by the window.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg.Normalize()
	return cfg, cfg.Validate()
}

// Save writes the file atomically: a crash halfway never leaves a broken
// file behind.
func Save(path string, cfg Config) error {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'))
}

// WriteFileAtomic writes data to a temporary file next to path and renames
// it into place.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Set changes one value by its JSON name, as used by "ntp-sync config set".
// Nothing changes unless the result is valid.
func (c *Config) Set(key, value string) error {
	next := *c
	if err := next.set(key, value); err != nil {
		return err
	}
	*c = next
	return nil
}

func (c *Config) set(key, value string) error {
	value = strings.TrimSpace(value)
	parseBool := func() (bool, error) {
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on", "ja":
			return true, nil
		case "false", "0", "no", "off", "nein":
			return false, nil
		}
		return false, fmt.Errorf("%s expects true or false", key)
	}
	parseInt := func() (int, error) {
		n, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("%s expects a number", key)
		}
		return n, nil
	}
	var err error
	switch strings.ToLower(key) {
	case "servers":
		c.Servers = strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	case "intervalminutes", "interval":
		c.IntervalMinutes, err = parseInt()
	case "synconstart":
		c.SyncOnStart, err = parseBool()
	case "synconresume":
		c.SyncOnResume, err = parseBool()
	case "synconnetworkchange":
		c.SyncOnNetworkChange, err = parseBool()
	case "mincorrectionms":
		c.MinCorrectionMs, err = parseInt()
	case "slewmaxms":
		c.SlewMaxMs, err = parseInt()
	case "bigjumpminutes":
		c.BigJumpMinutes, err = parseInt()
	case "httpsfallback":
		c.HTTPSFallback, err = parseBool()
	case "httpsfallbackurls":
		c.HTTPSFallbackURLs = strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	case "w32timemode":
		c.W32TimeMode = strings.ToLower(value)
	case "allowondomain":
		c.AllowOnDomain, err = parseBool()
	case "eventlog":
		c.EventLog, err = parseBool()
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	if err != nil {
		return err
	}
	c.Normalize()
	return c.Validate()
}
