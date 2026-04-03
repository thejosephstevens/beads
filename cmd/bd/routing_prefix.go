package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/debug"
)

// routeEntry represents a single prefix-to-path mapping from routes.jsonl.
type routeEntry struct {
	Prefix string `json:"prefix"`
	Path   string `json:"path"`
}

// findRoutesFile locates the routes.jsonl file for prefix-based routing.
// Checks GT_ROOT first (orchestrator mode), then walks up from CWD.
func findRoutesFile() string {
	if gtRoot := os.Getenv("GT_ROOT"); gtRoot != "" {
		routesPath := filepath.Join(gtRoot, ".beads", "routes.jsonl")
		if _, err := os.Stat(routesPath); err == nil {
			return routesPath
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		routesPath := filepath.Join(dir, ".beads", "routes.jsonl")
		if _, err := os.Stat(routesPath); err == nil {
			return routesPath
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return ""
}

// loadRoutes parses a routes.jsonl file into route entries.
// Each line is a JSON object with "prefix" and "path" fields.
func loadRoutes(routesPath string) ([]routeEntry, error) {
	f, err := os.Open(routesPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var routes []routeEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry routeEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Prefix != "" && entry.Path != "" {
			routes = append(routes, entry)
		}
	}
	return routes, scanner.Err()
}

// matchRouteForID finds the best matching route for an issue ID.
// Handles both prefix formats: "ga" (no dash) and "gt-" (with dash).
// Returns the longest matching route, or nil if no match.
func matchRouteForID(routes []routeEntry, id string) *routeEntry {
	var best *routeEntry
	bestLen := 0
	for i := range routes {
		prefix := routes[i].Prefix
		// Normalize: ensure we match prefix followed by dash
		matchPrefix := prefix
		if !strings.HasSuffix(matchPrefix, "-") {
			matchPrefix += "-"
		}
		if strings.HasPrefix(id, matchPrefix) && len(matchPrefix) > bestLen {
			best = &routes[i]
			bestLen = len(matchPrefix)
		}
	}
	return best
}

// resolveViaPrefixRouting attempts to find an issue using prefix-based routing
// via routes.jsonl. Extracts the prefix from the issue ID, finds the matching
// route, and opens the target rig's store.
func resolveViaPrefixRouting(ctx context.Context, id string) (*RoutedResult, error) {
	routesPath := findRoutesFile()
	if routesPath == "" {
		return nil, fmt.Errorf("no routes.jsonl found")
	}

	routes, err := loadRoutes(routesPath)
	if err != nil {
		return nil, fmt.Errorf("loading routes: %w", err)
	}

	route := matchRouteForID(routes, id)
	if route == nil {
		return nil, fmt.Errorf("no route matches prefix for %q", id)
	}

	// Paths in routes.jsonl are relative to the town root.
	// routes.jsonl lives at <town>/.beads/routes.jsonl
	townRoot := filepath.Dir(filepath.Dir(routesPath))
	targetPath := route.Path
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(townRoot, targetPath)
	}

	// Skip if route points to current directory (already tried local store)
	if targetPath == "." {
		return nil, fmt.Errorf("route points to current directory")
	}

	targetBeadsDir := filepath.Join(targetPath, ".beads")
	debug.Logf("prefix routing: %q matched prefix=%q -> %s\n", id, route.Prefix, targetBeadsDir)

	routedStore, err := newReadOnlyStoreFromConfig(ctx, targetBeadsDir)
	if err != nil {
		return nil, fmt.Errorf("opening routed store at %s: %w", targetPath, err)
	}

	result, err := resolveAndGetFromStore(ctx, routedStore, id, true)
	if err != nil {
		_ = routedStore.Close()
		return nil, err
	}
	result.closeFn = func() { _ = routedStore.Close() }
	return result, nil
}
