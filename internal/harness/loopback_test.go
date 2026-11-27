package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devex/harness/internal/config"
)

// A child that asks for every interface, in each listen() shape, must end up on loopback.
func TestLoopbackPreloadPinsListeners(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	home := filepath.Join(t.TempDir(), "dir with space")
	os.MkdirAll(home, 0o755)
	tool, err := (&Runtime{home: home}).loopbackOnly(config.Tool{ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "-e", `
const net = require('net');
const shapes = [[0], [0, undefined], [0, '0.0.0.0'], [{ port: 0, host: '::' }]];
let left = shapes.length;
for (const a of shapes) {
  const s = net.createServer();
  s.listen(...a, () => { console.log(s.address().address); s.close(); });
}`)
	cmd.Env = append(os.Environ(), "NODE_OPTIONS="+tool.Env["NODE_OPTIONS"])
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := strings.Fields(string(out)); len(got) != 4 || strings.Join(got, " ") != strings.TrimSpace(strings.Repeat("127.0.0.1 ", 4)) {
		t.Fatalf("addresses = %q, want four 127.0.0.1", got)
	}
}
