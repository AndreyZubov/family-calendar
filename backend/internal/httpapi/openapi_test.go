package httpapi

import (
	"bufio"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// specRoutes extracts "METHOD /path" pairs from openapi.yaml. A tiny line
// parser is enough for our layout and avoids a YAML dependency:
// paths are at 2-space indent, operations at 4.
func specRoutes(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	pathRe := regexp.MustCompile(`^  (/\S*):\s*$`)
	opRe := regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
	var routes []string
	var current string
	inPaths := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "paths:":
			inPaths = true
		case inPaths && line != "" && !strings.HasPrefix(line, " "):
			inPaths = false // next top-level key
		case inPaths && pathRe.MatchString(line):
			current = pathRe.FindStringSubmatch(line)[1]
		case inPaths && current != "" && opRe.MatchString(line):
			routes = append(routes, strings.ToUpper(opRe.FindStringSubmatch(line)[1])+" "+current)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return routes
}

func TestOpenAPIMatchesRouter(t *testing.T) {
	spec := specRoutes(t)
	if len(spec) < 10 {
		t.Fatalf("parsed only %d routes from openapi.yaml; parser broken?", len(spec))
	}
	api, _ := newTestAPI(t, fakeDB{})
	registered := api.patterns

	slices.Sort(spec)
	slices.Sort(registered)
	for _, r := range registered {
		if !slices.Contains(spec, r) {
			t.Errorf("route %q is registered but missing from openapi.yaml", r)
		}
	}
	for _, r := range spec {
		if !slices.Contains(registered, r) {
			t.Errorf("route %q is in openapi.yaml but not registered", r)
		}
	}
}
