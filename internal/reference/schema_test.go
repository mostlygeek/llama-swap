package reference

import (
	"testing"
	"testing/fstest"
)

func TestDocs_SchemaFragment_Root(t *testing.T) {
	got, ok := New(testFS()).SchemaFragment("")
	if !ok {
		t.Fatal("SchemaFragment(\"\") ok = false")
	}
	if got.Type != "object" {
		t.Errorf("Type = %q, want object", got.Type)
	}
	if len(got.Required) != 1 || got.Required[0] != "models" {
		t.Errorf("Required = %v, want [models]", got.Required)
	}

	names := propNames(got.Properties)
	for _, want := range []string{"alpha", "logLevel", "models"} {
		if !names[want] {
			t.Errorf("root properties missing %q, got %v", want, names)
		}
	}
	if !names["models"] {
		t.Fatal("models missing")
	}
	for _, prop := range got.Properties {
		if prop.Name == "models" && !prop.Required {
			t.Error("models should be marked required")
		}
	}
}

func TestDocs_SchemaFragment_DotAndWhitespaceAreTheRoot(t *testing.T) {
	d := New(testFS())
	for _, path := range []string{"", ".", "  ", " . "} {
		got, ok := d.SchemaFragment(path)
		if !ok || got.Type != "object" {
			t.Errorf("SchemaFragment(%q) = (%+v, %v), want the root object", path, got, ok)
		}
	}
}

func TestDocs_SchemaFragment_LeafCarriesMetadata(t *testing.T) {
	got, ok := New(testFS()).SchemaFragment("alpha")
	if !ok {
		t.Fatal("SchemaFragment(alpha) ok = false")
	}
	if got.Path != "alpha" {
		t.Errorf("Path = %q", got.Path)
	}
	if got.Type != "integer" {
		t.Errorf("Type = %q, want integer", got.Type)
	}
	if got.Description != "The first setting." {
		t.Errorf("Description = %q", got.Description)
	}
	if string(got.Default) != "1" {
		t.Errorf("Default = %s, want 1", got.Default)
	}
}

func TestDocs_SchemaFragment_CarriesEnum(t *testing.T) {
	got, _ := New(testFS()).SchemaFragment("logLevel")
	if string(got.Enum) != `["debug","info"]` {
		t.Errorf("Enum = %s", got.Enum)
	}
}

func TestDocs_SchemaFragment_WildcardDescendsAdditionalProperties(t *testing.T) {
	d := New(testFS())

	models, _ := d.SchemaFragment("models")
	if !models.MapKeys {
		t.Error("models.MapKeys = false, want true (children are addressed with *)")
	}
	if len(models.Properties) != 0 {
		t.Errorf("models has direct properties %v, want none", propNames(models.Properties))
	}

	entry, ok := d.SchemaFragment("models.*")
	if !ok {
		t.Fatal("SchemaFragment(models.*) ok = false")
	}
	names := propNames(entry.Properties)
	for _, want := range []string{"cmd", "filters", "timeouts"} {
		if !names[want] {
			t.Errorf("models.* missing %q, got %v", want, names)
		}
	}

	leaf, ok := d.SchemaFragment("models.*.cmd")
	if !ok || leaf.Description != "The command to run." {
		t.Errorf("SchemaFragment(models.*.cmd) = (%+v, %v)", leaf, ok)
	}
}

func TestDocs_SchemaFragment_ResolvesDefinitionRef(t *testing.T) {
	got, ok := New(testFS()).SchemaFragment("models.*.timeouts")
	if !ok {
		t.Fatal("SchemaFragment(models.*.timeouts) ok = false")
	}
	if got.Description != "Connection timeouts." {
		t.Errorf("Description = %q, want the resolved definition", got.Description)
	}
	if !propNames(got.Properties)["connect"] {
		t.Errorf("Properties = %v, want connect from the definition", propNames(got.Properties))
	}
}

// Only one level of properties is expanded: that is the size cap keeping a
// fragment from filling a small model's context window.
func TestDocs_SchemaFragment_ExpandsOnlyOneLevel(t *testing.T) {
	got, _ := New(testFS()).SchemaFragment("models.*")
	for _, prop := range got.Properties {
		if prop.Name != "filters" {
			continue
		}
		if prop.Type != "object" {
			t.Errorf("filters Type = %q, want object", prop.Type)
		}
		return
	}
	t.Fatal("filters property missing")
}

func TestDocs_SchemaFragment_TruncatesToFirstDescriptionLine(t *testing.T) {
	got, _ := New(testFS()).SchemaFragment("models.*.filters")
	for _, prop := range got.Properties {
		if prop.Name == "stripParams" {
			if prop.Description != "Params to remove." {
				t.Errorf("Description = %q, want only the first line", prop.Description)
			}
			return
		}
	}
	t.Fatal("stripParams property missing")
}

func TestDocs_SchemaFragment_UnknownPathReturnsFalse(t *testing.T) {
	d := New(testFS())
	for _, path := range []string{"nope", "models.nope", "models.*.nope", "alpha.*", "alpha.deeper"} {
		if _, ok := d.SchemaFragment(path); ok {
			t.Errorf("SchemaFragment(%q) ok = true, want false", path)
		}
	}
}

func TestDocs_SchemaFragment_MissingSchemaFile(t *testing.T) {
	fsys := testFS()
	delete(fsys, "config-schema.json")

	d := New(fsys)
	if !d.Enabled() {
		t.Fatal("docs should still index without a schema file")
	}
	if _, ok := d.SchemaFragment("alpha"); ok {
		t.Error("SchemaFragment ok = true without a schema file")
	}
	if got := d.SchemaPaths(""); got != nil {
		t.Errorf("SchemaPaths = %v, want nil", got)
	}
}

func TestDocs_SchemaFragment_MalformedSchemaFile(t *testing.T) {
	fsys := testFS()
	fsys["config-schema.json"] = &fstest.MapFile{Data: []byte("{not json")}

	d := New(fsys)
	if !d.Enabled() {
		t.Fatal("docs should still index with a malformed schema")
	}
	if _, ok := d.SchemaFragment("alpha"); ok {
		t.Error("SchemaFragment ok = true with a malformed schema")
	}
}

func TestDocs_SchemaPaths(t *testing.T) {
	d := New(testFS())

	tests := []struct {
		name   string
		prefix string
		want   []string
	}{
		{"root lists top-level keys", "", []string{"alpha", "logLevel", "models"}},
		{"a map lists the wildcard", "models", []string{"models.*"}},
		{"an object lists its children", "models.*", []string{"models.*.cmd", "models.*.filters", "models.*.timeouts"}},
		{"an unknown prefix falls back to the root", "nope", []string{"alpha", "logLevel", "models"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.SchemaPaths(tt.prefix)
			if len(got) != len(tt.want) {
				t.Fatalf("SchemaPaths(%q) = %v, want %v", tt.prefix, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("SchemaPaths(%q) = %v, want %v", tt.prefix, got, tt.want)
				}
			}
		})
	}
}

func propNames(props []SchemaProp) map[string]bool {
	out := map[string]bool{}
	for _, prop := range props {
		out[prop.Name] = true
	}
	return out
}
