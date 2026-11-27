package config

import "testing"

func TestImproveDefaults(t *testing.T) {
	c := &Config{Schema: SchemaVersion}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Improve.Proposer != "claude" {
		t.Fatalf("proposer = %q, want %q", c.Improve.Proposer, "claude")
	}
	if c.Improve.K != 3 {
		t.Fatalf("k = %d, want 3", c.Improve.K)
	}
}

func TestImproveKRange(t *testing.T) {
	p := writeCfg(t, `{"$schema":"harness@1","profile":"core","listen":"127.0.0.1:4400","improve":{"proposer":"claude","k":51}}`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for k=51")
	}

	p = writeCfg(t, `{"$schema":"harness@1","profile":"core","listen":"127.0.0.1:4400","improve":{"proposer":"claude","k":1}}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Improve.K != 1 {
		t.Fatalf("k = %d, want 1", c.Improve.K)
	}

	p = writeCfg(t, `{"$schema":"harness@1","profile":"core","listen":"127.0.0.1:4400","improve":{"proposer":"claude","k":0}}`)
	c, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Improve.K != 3 {
		t.Fatalf("k = %d, want 3 (default)", c.Improve.K)
	}
}

func TestLoadImproveRoundTrip(t *testing.T) {
	p := writeCfg(t, `{
		"$schema":"harness@1","level":"beginner","profile":"core","listen":"127.0.0.1:4400",
		"project":{"root":".","app_ports":[8080]},
		"tools":[{"id":"map","tab":"Map","profile":"core","managed":false}],
		"improve":{"proposer":"claude","k":3}
	}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Improve.Proposer != "claude" {
		t.Fatalf("proposer = %q, want %q", c.Improve.Proposer, "claude")
	}
	if c.Improve.K != 3 {
		t.Fatalf("k = %d, want 3", c.Improve.K)
	}
}
