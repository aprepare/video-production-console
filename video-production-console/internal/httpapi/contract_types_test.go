package httpapi

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"video-production-console/internal/domain"
)

// Go response structs and the frontend types that read them are both written by
// hand, with no code generation between them. This test pins the pairs so a
// rename on either side fails here instead of silently becoming `undefined` in
// the browser.
//
// The asserted direction is deliberate: **every TS field must exist on the Go
// side**, while Go may carry keys the frontend ignores. A TS-only field means
// the frontend reads a key nobody sends; an unused Go key is merely payload
// weight. Requiring an exact match would force the frontend to declare fields
// it has no use for.
var responseContracts = []struct {
	name   string
	goType any
	tsFile string
	tsType string
}{
	{"task phase run", domain.TaskPhaseRun{}, "types.ts", "TaskPhaseRun"},
	{"task timing summary", domain.TaskTimingSummary{}, "types.ts", "TaskTimingSummary"},
	{"task event", domain.TaskEvent{}, "tasks/event-types.ts", "TaskEvent"},
	{"semantic event", domain.SemanticEvent{}, "tasks/event-types.ts", "SemanticEvent"},
	{"project", projectView{}, "project-workbench/types.ts", "ProjectSummary"},
}

func TestResponseTypesCoverFrontendFields(t *testing.T) {
	for _, contract := range responseContracts {
		t.Run(contract.name, func(t *testing.T) {
			goKeys := goJSONKeys(t, reflect.TypeOf(contract.goType))
			for _, key := range slices.Sorted(maps.Keys(goKeys)) {
				// An untagged exported field is recorded under its Go name, so
				// this also rejects structs serialized without json tags.
				if key != strings.ToLower(key) {
					t.Errorf("%s exposes non snake_case key %q; add a json tag (or a view struct) before the frontend has to read it", contract.name, key)
				}
			}
			tsFields := parseTSObjectFields(t, contract.tsFile, contract.tsType)
			if len(tsFields) == 0 {
				t.Fatalf("parsed no fields from %s in %s; the parser or the type shape changed", contract.tsType, contract.tsFile)
			}
			for _, field := range tsFields {
				if _, ok := goKeys[field]; !ok {
					t.Errorf("%s: %s.%s has no matching Go json key; the frontend would read undefined. Go keys: %v",
						contract.name, contract.tsType, field, slices.Sorted(maps.Keys(goKeys)))
				}
			}
		})
	}
}

// goJSONKeys returns the object keys a type serializes to. Untagged exported
// fields map to their Go field name, mirroring encoding/json, so the caller can
// detect PascalCase leaks.
func goJSONKeys(t *testing.T, typ reflect.Type) map[string]struct{} {
	t.Helper()
	if typ.Kind() != reflect.Struct {
		t.Fatalf("expected a struct, got %s", typ.Kind())
	}
	keys := map[string]struct{}{}
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if field.Anonymous && tag == "" {
			for key := range goJSONKeys(t, field.Type) {
				keys[key] = struct{}{}
			}
			continue
		}
		if tag == "" {
			tag = field.Name
		}
		keys[tag] = struct{}{}
	}
	return keys
}

// tsFieldPattern matches a field declared at the top level of an object type
// literal. The two-space indent requirement keeps nested literals out.
var tsFieldPattern = regexp.MustCompile(`^  ([A-Za-z_][A-Za-z0-9_]*)\??:`)

func parseTSObjectFields(t *testing.T, relPath, typeName string) []string {
	t.Helper()
	path := filepath.Join("..", "..", "web", "src", filepath.FromSlash(relPath))
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	header := "export type " + typeName + " = {"
	_, after, found := strings.Cut(strings.ReplaceAll(string(source), "\r\n", "\n"), header+"\n")
	if !found {
		// Composed types (Omit<...> & {...}) have no single literal to compare;
		// pair the underlying plain type instead of loosening the parser.
		t.Fatalf("%s does not declare %q as a plain object type literal", relPath, typeName)
	}
	body, _, found := strings.Cut(after, "\n};")
	if !found {
		t.Fatalf("%s: unterminated object literal for %q", relPath, typeName)
	}
	var fields []string
	for _, line := range strings.Split(body, "\n") {
		if match := tsFieldPattern.FindStringSubmatch(line); match != nil {
			fields = append(fields, match[1])
		}
	}
	return fields
}

// TestContractGuardsDetectDrift exercises the two failure modes on fixtures, so
// a future refactor cannot quietly turn the guard above into a no-op.
func TestContractGuardsDetectDrift(t *testing.T) {
	untagged := goJSONKeys(t, reflect.TypeOf(struct {
		AccountID string
		ID        string `json:"id"`
		Skipped   string `json:"-"`
	}{}))
	if _, ok := untagged["AccountID"]; !ok {
		t.Fatal("untagged exported field should surface under its Go name")
	}
	if _, ok := untagged["Skipped"]; ok {
		t.Fatal(`json:"-" field should not surface`)
	}
	if _, ok := untagged["id"]; !ok {
		t.Fatal("tagged field should surface under its tag")
	}
	fields := parseTSObjectFields(t, "tasks/event-types.ts", "SemanticEvent")
	if !slices.Contains(fields, "created_at") || slices.Contains(fields, "raw_json") {
		t.Fatalf("TS parser returned unexpected fields: %v", fields)
	}
}
