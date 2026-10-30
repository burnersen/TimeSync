package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateServer(t *testing.T) {
	good := []string{"pool.ntp.org", "ptbtime1.ptb.de", "192.0.2.1", "192.0.2.1:1234", "[2001:db8::1]:123",
		"2001:db8::1", "nts://time.cloudflare.com", "nts://ptbtime1.ptb.de:4460", "fritz.box", "ntp-1.example"}
	bad := []string{"", "pool ntp org", "http://pool.ntp.org", "host:0", "host:99999", "host:abc", "-bad.example",
		"bad-.example", "a..b", "ex@mple.org", "nts://", `C:\evil`}
	for _, s := range good {
		if err := ValidateServer(s); err != nil {
			t.Errorf("%q rejected: %v", s, err)
		}
	}
	for _, s := range bad {
		if ValidateServer(s) == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.json")

	cfg, err := Load(path)
	if err != nil || cfg.IntervalMinutes != 60 {
		t.Fatalf("missing file must give defaults: %+v %v", cfg, err)
	}
	cfg.Servers = []string{" PTBTIME1.ptb.de ", "ptbtime1.ptb.de", "", "nts://time.cloudflare.com"}
	cfg.IntervalMinutes = 30
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Servers) != 2 || back.Servers[0] != "ptbtime1.ptb.de" || back.IntervalMinutes != 30 {
		t.Fatalf("round trip: %+v", back)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestLoadDamagedFileFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte("{ this is not json"), 0o644)
	cfg, err := Load(path)
	if err == nil || cfg.IntervalMinutes != 60 {
		t.Fatalf("want defaults plus error, got %+v %v", cfg, err)
	}
	os.WriteFile(path, []byte(`{"intervalMinutes": 99999}`), 0o644)
	cfg, err = Load(path)
	if err == nil || cfg.IntervalMinutes != 60 {
		t.Fatalf("invalid values must not be used: %+v %v", cfg, err)
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"intervalMinutes": 15}`), 0o644)
	cfg, err := Load(path)
	if err != nil || cfg.IntervalMinutes != 15 || !cfg.SyncOnResume || len(cfg.Servers) != 4 {
		t.Fatalf("%+v %v", cfg, err)
	}
}

func TestSet(t *testing.T) {
	c := Default()
	if err := c.Set("servers", "ptbtime1.ptb.de, ptbtime2.ptb.de"); err != nil || len(c.Servers) != 2 {
		t.Fatalf("%v %v", c.Servers, err)
	}
	if err := c.Set("interval", "3"); err == nil {
		t.Fatal("3 minutes accepted")
	}
	if err := c.Set("allowOnDomain", "ja"); err != nil || !c.AllowOnDomain {
		t.Fatal("bool not parsed")
	}
	if err := c.Set("w32timeMode", "explode"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if err := c.Set("nonsense", "1"); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestOldShortIntervalIsRaised(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"intervalMinutes": 5, "servers": ["ptbtime1.ptb.de"]}`), 0o644)
	cfg, err := Load(path)
	if err != nil || cfg.IntervalMinutes != MinIntervalMinutes || cfg.Servers[0] != "ptbtime1.ptb.de" {
		t.Fatalf("%+v %v", cfg, err)
	}
}
