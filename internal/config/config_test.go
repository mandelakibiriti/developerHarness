package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "harness.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	p := writeCfg(t, `{
		"$schema":"harness@1","level":"beginner","profile":"core","listen":"127.0.0.1:4400",
		"project":{"root":".","app_ports":[8080]},
		"tools":[{"id":"map","tab":"Map","profile":"core","managed":false,"port_from":{"file":"f.log","regex":"(http://\\S+)"}}],
		"budgets":{"per_feature_usd":2.0,"alert_at":[0.8,1.0]}
	}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Profile != "core" || c.Listen != "127.0.0.1:4400" {
		t.Fatalf("unexpected config: %+v", c)
	}
	if len(c.Tools) != 1 || c.Tools[0].ID != "map" {
		t.Fatalf("unexpected tools: %+v", c.Tools)
	}
	if c.Tools[0].IsManaged() {
		t.Fatal("map should be unmanaged")
	}
}

func TestValidateRejectsBadSchema(t *testing.T) {
	p := writeCfg(t, `{"$schema":"nope","profile":"core","listen":"127.0.0.1:4400"}`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for bad schema")
	}
}

func TestValidateRejectsCycle(t *testing.T) {
	p := writeCfg(t, `{
		"$schema":"harness@1","profile":"core","listen":"127.0.0.1:4400",
		"tools":[
			{"id":"a","tab":"A","profile":"core","command":["sleep","1"],"depends_on":["b"]},
			{"id":"b","tab":"B","profile":"core","command":["sleep","1"],"depends_on":["a"]}
		]}`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestActiveToolsProfileFiltering(t *testing.T) {
	p := writeCfg(t, `{
		"$schema":"harness@1","profile":"core","listen":"127.0.0.1:4400",
		"tools":[
			{"id":"core1","tab":"C","profile":"core","command":["sleep","1"]},
			{"id":"obs1","tab":"O","profile":"observe","command":["sleep","1"]},
			{"id":"sec1","tab":"S","profile":"security","command":["sleep","1"]}
		]}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if core, _ := c.ActiveTools("core"); len(core) != 1 || core[0].ID != "core1" {
		t.Fatalf("core: %+v", core)
	}
	if obs, _ := c.ActiveTools("observe"); len(obs) != 2 {
		t.Fatalf("observe should include core+observe: %+v", obs)
	}
	if sec, _ := c.ActiveTools("security"); len(sec) != 3 {
		t.Fatalf("security should include all: %+v", sec)
	}
}

func TestTopoSortOrder(t *testing.T) {
	tools := []Tool{
		{ID: "b", Profile: "core", DependsOn: []string{"a"}},
		{ID: "a", Profile: "core"},
	}
	order, err := TopoSort(tools)
	if err != nil {
		t.Fatal(err)
	}
	if order[0].ID != "a" || order[1].ID != "b" {
		t.Fatalf("order: %+v", order)
	}
}

func TestExpand(t *testing.T) {
	t.Setenv("FOO", "bar")
	if got := Expand("x${FOO}y", "/tools"); got != "xbary" {
		t.Fatalf("got %q", got)
	}
	if got := Expand("${HARNESS_TOOLS}/tokentelemetry", "/tools"); got != "/tools/tokentelemetry" {
		t.Fatalf("got %q", got)
	}
	if got := Expand("${UNSET_VAR}", "/tools"); got != "${UNSET_VAR}" {
		t.Fatalf("unset var should be left alone, got %q", got)
	}
}
