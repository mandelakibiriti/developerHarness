package harness

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestAppsDetectAndLaunch(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Fake.app"), 0o755)
	t.Setenv("HARNESS_TOOLS", t.TempDir())
	origCat, origDirs, origOpen := appCatalog, appDirs, openCommand
	t.Cleanup(func() { appCatalog, appDirs, openCommand = origCat, origDirs, origOpen })
	appCatalog = []appSpec{
		{id: "fake", name: "Fake", app: "Fake"},
		{id: "cli", name: "CLI", bin: "sh"},
		{id: "missing", name: "Missing", app: "Nope", bin: "definitely-not-installed"},
	}
	appDirs = func() []string { return []string{dir} }
	openCommand = "true"
	h, _ := newTestRuntime(t, t.TempDir())

	var got struct{ Apps []appStatus }
	json.Unmarshal(serveJSON(h, http.MethodGet, "/api/apps", "").Body.Bytes(), &got)
	if len(got.Apps) != 2 || !got.Apps[0].Launchable || got.Apps[1].Launchable {
		t.Fatalf("apps = %+v, want installed fake (launchable) and cli (not)", got.Apps)
	}
	for body, want := range map[string]int{
		`{"id":"fake"}`:      http.StatusOK,
		`{"id":"cli"}`:       http.StatusBadRequest,
		`{"id":"missing"}`:   http.StatusBadRequest,
		`{"id":"/bin/sh"}`:   http.StatusNotFound,
		`{"path":"/bin/sh"}`: http.StatusNotFound,
	} {
		if rec := serveJSON(h, http.MethodPost, "/api/apps", body); rec.Code != want {
			t.Errorf("POST %s = %d, want %d", body, rec.Code, want)
		}
	}
}
