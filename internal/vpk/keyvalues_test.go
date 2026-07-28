package vpk

import (
	"encoding/json"
	"testing"
)

func TestParseKeyValuesJSON(t *testing.T) {
	input := []byte(`
"root"
{
	"name" "Counter-Strike"
	"nested"
	{
		"value" "123"
	}
	"dup" "one"
	"dup" "two"
}
`)

	output, err := parseKeyValuesJSON(input)
	if err != nil {
		t.Fatalf("parseKeyValuesJSON() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	root, ok := decoded["root"].(map[string]any)
	if !ok {
		t.Fatalf("root type = %T, want map[string]any", decoded["root"])
	}

	if got := root["name"]; got != "Counter-Strike" {
		t.Fatalf("name = %v, want Counter-Strike", got)
	}

	nested, ok := root["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested type = %T, want map[string]any", root["nested"])
	}

	if got := nested["value"]; got != "123" {
		t.Fatalf("nested.value = %v, want 123", got)
	}

	dup, ok := root["dup"].([]any)
	if !ok {
		t.Fatalf("dup type = %T, want []any", root["dup"])
	}

	if len(dup) != 2 || dup[0] != "one" || dup[1] != "two" {
		t.Fatalf("dup = %#v, want [one two]", dup)
	}
}
