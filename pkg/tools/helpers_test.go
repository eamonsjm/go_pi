package tools

import "testing"

func TestGetInt(t *testing.T) {
	tests := []struct {
		name   string
		val    any
		want   int
		wantOK bool
	}{
		{"float64", float64(42), 42, true},
		{"int", int(7), 7, true},
		{"int64", int64(99), 99, true},
		{"string", "nope", 0, false},
		{"nil", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{"key": tt.val}
			got, ok := getInt(params, "key")
			if ok != tt.wantOK {
				t.Errorf("getInt ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("getInt = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetInt_MissingKey(t *testing.T) {
	params := map[string]any{}
	_, ok := getInt(params, "missing")
	if ok {
		t.Error("expected false for missing key")
	}
}

func TestGetString(t *testing.T) {
	params := map[string]any{"name": "hello"}
	got, ok := getString(params, "name")
	if !ok || got != "hello" {
		t.Errorf("getString = %q, %v; want %q, true", got, ok, "hello")
	}

	_, ok = getString(params, "missing")
	if ok {
		t.Error("expected false for missing key")
	}

	params2 := map[string]any{"num": 42}
	_, ok = getString(params2, "num")
	if ok {
		t.Error("expected false for non-string value")
	}
}
