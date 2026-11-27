package harness

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/devex/harness/internal/config"
)

// Some Node tools listen on every interface with no setting to change it
// (agent-trail's ingest port serves session transcripts). For tools marked
// "loopback_only" this preload pins every TCP listener to 127.0.0.1; the tool
// itself stays unmodified.
const loopbackPreload = `'use strict';
const net = require('net');
const listen = net.Server.prototype.listen;
net.Server.prototype.listen = function (...args) {
  const [first, second] = args;
  if (typeof first === 'number' || /^\d+$/.test(first)) {
    if (typeof second === 'string' || (second === undefined && args.length > 1)) args[1] = '127.0.0.1';
    else args.splice(1, 0, '127.0.0.1');
  } else if (first && typeof first === 'object' && first.port !== undefined) {
    args[0] = { ...first, host: '127.0.0.1' };
  }
  return listen.apply(this, args);
};
`

// loopbackOnly returns t with NODE_OPTIONS requiring the preload.
func (rt *Runtime) loopbackOnly(t config.Tool) (config.Tool, error) {
	path, err := filepath.Abs(filepath.Join(rt.home, "loopback.cjs"))
	if err != nil {
		return t, err
	}
	if err := os.WriteFile(path, []byte(loopbackPreload), 0o600); err != nil {
		return t, err
	}
	env := map[string]string{}
	for k, v := range t.Env {
		env[k] = v
	}
	quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path) + `"`
	env["NODE_OPTIONS"] = strings.TrimSpace(os.Getenv("NODE_OPTIONS") + " " + env["NODE_OPTIONS"] + " --require " + quoted)
	t.Env = env
	return t, nil
}
