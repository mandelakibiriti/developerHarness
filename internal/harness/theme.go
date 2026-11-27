package harness

import (
	_ "embed"
	"net"

	"github.com/devex/harness/internal/config"
)

// A tool with "theme": "shadcn" in harness.json is shown through
// <id>.localhost, where the proxy adds the harness's look to its pages. The tool
// itself stays unmodified.

//go:embed themes/trail.css
var trailCSS string

// themeScript follows the harness's light/dark choice. It clicks the tool's own
// theme button (labelled with the theme it switches to) so the tool's state
// stays consistent, and sets the attribute only if there is no such button.
const themeScript = `<script>
(function () {
  var wanted, tries = 0, timer;
  function apply() {
    clearTimeout(timer);
    var root = document.documentElement;
    if (root.getAttribute('data-theme') === wanted) return;
    var button = [].find.call(document.querySelectorAll('button'), function (b) { return b.textContent.trim().toLowerCase() === wanted; });
    if (button) button.click();
    if (++tries < 20) timer = setTimeout(apply, 300); // retries until the page is interactive
    else if (!button) root.setAttribute('data-theme', wanted);
  }
  addEventListener('message', function (e) {
    var t = e.source === parent && e.data && e.data.harnessTheme;
    if (t === 'light' || t === 'dark') { wanted = t; tries = 0; apply(); }
  });
})();
</script>`

var themes = map[string]func() string{"trail": func() string { return trailCSS }}

// themeSnippet is what the proxy adds to a themed tool's HTML ("" for others).
func (rt *Runtime) themeSnippet(sub string) string {
	css, known := themes[sub]
	if !known || !rt.themed(sub) {
		return ""
	}
	return "<style data-harness-theme>" + css() + "</style>" + themeScript
}

func (rt *Runtime) themed(id string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, t := range rt.tools {
		if t.ID == id {
			return t.Theme == config.ThemeShadcn
		}
	}
	return false
}

// embedURL is where the shell should frame a themed tool.
func (rt *Runtime) embedURL(id string) string {
	_, port, err := net.SplitHostPort(rt.cfg.Listen)
	if _, known := themes[id]; !known || err != nil {
		return ""
	}
	return "http://" + id + ".localhost:" + port + "/"
}
