package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchRouteForID(t *testing.T) {
	t.Parallel()

	routes := []routeEntry{
		{Prefix: "ga", Path: "../gascity"},
		{Prefix: "be", Path: "../beads"},
		{Prefix: "gt-", Path: "rig"},       // trailing dash format
		{Prefix: "gc", Path: "."},           // self-referential
		{Prefix: "gascity", Path: "../gas"}, // longer prefix to test longest-match
	}

	tests := []struct {
		name       string
		id         string
		wantPrefix string
		wantNil    bool
	}{
		{name: "match prefix without dash", id: "ga-ki2", wantPrefix: "ga"},
		{name: "match prefix with dash", id: "gt-child1", wantPrefix: "gt-"},
		{name: "match beads prefix", id: "be-nlc", wantPrefix: "be"},
		{name: "no match", id: "xx-unknown", wantNil: true},
		{name: "empty id", id: "", wantNil: true},
		{name: "id without dash", id: "nodash", wantNil: true},
		{name: "self-referential route", id: "gc-abc", wantPrefix: "gc"},
		{name: "longest prefix wins", id: "gascity-abc", wantPrefix: "gascity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := matchRouteForID(routes, tt.id)
			if tt.wantNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.wantPrefix, result.Prefix)
			}
		})
	}
}

func TestLoadRoutes(t *testing.T) {
	t.Parallel()

	t.Run("valid routes file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		content := `{"prefix":"ga","path":"../gascity"}
{"prefix":"be","path":"../beads"}
{"prefix":"gt-","path":"rig"}
`
		routesPath := filepath.Join(dir, "routes.jsonl")
		require.NoError(t, os.WriteFile(routesPath, []byte(content), 0644))

		routes, err := loadRoutes(routesPath)
		require.NoError(t, err)
		assert.Len(t, routes, 3)
		assert.Equal(t, "ga", routes[0].Prefix)
		assert.Equal(t, "../gascity", routes[0].Path)
	})

	t.Run("skips malformed lines", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		content := `{"prefix":"ga","path":"../gascity"}
not json
{"prefix":"be","path":"../beads"}
`
		routesPath := filepath.Join(dir, "routes.jsonl")
		require.NoError(t, os.WriteFile(routesPath, []byte(content), 0644))

		routes, err := loadRoutes(routesPath)
		require.NoError(t, err)
		assert.Len(t, routes, 2)
	})

	t.Run("skips entries with empty prefix or path", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		content := `{"prefix":"","path":"../gascity"}
{"prefix":"ga","path":""}
{"prefix":"be","path":"../beads"}
`
		routesPath := filepath.Join(dir, "routes.jsonl")
		require.NoError(t, os.WriteFile(routesPath, []byte(content), 0644))

		routes, err := loadRoutes(routesPath)
		require.NoError(t, err)
		assert.Len(t, routes, 1)
		assert.Equal(t, "be", routes[0].Prefix)
	})

	t.Run("nonexistent file returns error", func(t *testing.T) {
		t.Parallel()
		_, err := loadRoutes("/nonexistent/routes.jsonl")
		assert.Error(t, err)
	})
}

func TestFindRoutesFile(t *testing.T) {
	// Cannot run in parallel — uses os.Chdir and env vars

	t.Run("finds via GT_ROOT", func(t *testing.T) {
		dir := t.TempDir()
		beadsDir := filepath.Join(dir, ".beads")
		require.NoError(t, os.MkdirAll(beadsDir, 0755))
		routesPath := filepath.Join(beadsDir, "routes.jsonl")
		require.NoError(t, os.WriteFile(routesPath, []byte(`{"prefix":"ga","path":"."}`+"\n"), 0644))

		t.Setenv("GT_ROOT", dir)
		result := findRoutesFile()
		assert.Equal(t, routesPath, result)
	})

	t.Run("finds via CWD walk-up", func(t *testing.T) {
		dir := t.TempDir()
		// Resolve symlinks (macOS: /var -> /private/var) so paths match os.Getwd()
		dir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)

		beadsDir := filepath.Join(dir, ".beads")
		require.NoError(t, os.MkdirAll(beadsDir, 0755))
		routesPath := filepath.Join(beadsDir, "routes.jsonl")
		require.NoError(t, os.WriteFile(routesPath, []byte(`{"prefix":"ga","path":"."}`+"\n"), 0644))

		// Create a subdirectory and cd into it
		subDir := filepath.Join(dir, "rig", "sub")
		require.NoError(t, os.MkdirAll(subDir, 0755))

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(subDir))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		t.Setenv("GT_ROOT", "") // clear GT_ROOT to test CWD fallback
		result := findRoutesFile()
		assert.Equal(t, routesPath, result)
	})

	t.Run("returns empty when no routes.jsonl exists", func(t *testing.T) {
		dir := t.TempDir()
		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(dir))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		t.Setenv("GT_ROOT", "")
		result := findRoutesFile()
		assert.Empty(t, result)
	})
}
