package vector

import (
	"strings"
	"testing"
)

func TestVectorValue(t *testing.T) {
	tests := []struct {
		v    Vector
		want string
	}{
		{Vector{1.0, 2.5, -3.0}, "[1,2.5,-3]"},
		{Vector{0.0}, "[0]"},
		{Vector{}, "[]"},
		{Vector{1.1234567}, "[1.1234567]"},
	}
	for _, tt := range tests {
		got, err := tt.v.Value()
		if err != nil {
			t.Fatalf("Value() error: %v", err)
		}
		gotStr, ok := got.(string)
		if !ok {
			t.Fatalf("Value() returned %T, want string", got)
		}
		if gotStr != tt.want {
			t.Errorf("Value() = %s, want %s", gotStr, tt.want)
		}
	}
}

func TestVectorScan(t *testing.T) {
	tests := []struct {
		input string
		want  Vector
	}{
		{"[1,2.5,-3]", Vector{1.0, 2.5, -3.0}},
		{"[0]", Vector{0.0}},
		{"[]", Vector{}},
		{"{1,2,3}", Vector{1.0, 2.0, 3.0}},
		{"", Vector{}},
	}
	for _, tt := range tests {
		var v Vector
		if err := v.Scan(tt.input); err != nil {
			t.Fatalf("Scan(%q) error: %v", tt.input, err)
		}
		if len(v) != len(tt.want) {
			t.Fatalf("Scan(%q) len = %d, want %d", tt.input, len(v), len(tt.want))
		}
		for i := range v {
			if v[i] != tt.want[i] {
				t.Errorf("Scan(%q)[%d] = %v, want %v", tt.input, i, v[i], tt.want[i])
			}
		}
	}
}

func TestVectorScan_Bytes(t *testing.T) {
	var v Vector
	if err := v.Scan([]byte("[1,2,3]")); err != nil {
		t.Fatalf("Scan([]byte) error: %v", err)
	}
	if len(v) != 3 || v[0] != 1.0 || v[1] != 2.0 || v[2] != 3.0 {
		t.Errorf("Scan([]byte) = %v, want [1 2 3]", v)
	}
}

func TestVectorScan_InvalidType(t *testing.T) {
	var v Vector
	if err := v.Scan(123); err == nil {
		t.Error("Scan(int) should return error")
	}
}

func TestVectorScan_InvalidFloat(t *testing.T) {
	var v Vector
	if err := v.Scan("[1,abc,3]"); err == nil {
		t.Error("Scan(invalid float) should return error")
	}
}

func TestVectorRoundTrip(t *testing.T) {
	original := Vector{1.5, -2.25, 3.0, 0.0}
	val, err := original.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}

	var parsed Vector
	if err := parsed.Scan(val); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(parsed) != len(original) {
		t.Fatalf("len mismatch: %d vs %d", len(parsed), len(original))
	}
	for i := range original {
		if parsed[i] != original[i] {
			t.Errorf("roundtrip[%d] = %v, want %v", i, parsed[i], original[i])
		}
	}
}

func TestVectorValue_Large(t *testing.T) {
	v := make(Vector, 1536)
	for i := range v {
		v[i] = float32(i) * 0.001
	}
	val, err := v.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}
	str := val.(string)
	if !strings.HasPrefix(str, "[") || !strings.HasSuffix(str, "]") {
		t.Errorf("Value() format error: %s", str)
	}
	// 检查逗号数量（应有 1535 个）
	if strings.Count(str, ",") != 1535 {
		t.Errorf("Value() comma count = %d, want 1535", strings.Count(str, ","))
	}
}
