package util

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestJWTAndAtomicData(t *testing.T) {
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"account-1","exp":1800000000}`)) + ".sig"
	if Float(JWTExp(token)) != 1800000000 || JWTPayload(token)["sub"] != "account-1" || len(JWTPayload("broken")) != 0 {
		t.Fatal("JWT decode")
	}
	if MaskPhone("13812345678") != "138****5678" {
		t.Fatal("phone mask")
	}
	file := filepath.Join(t.TempDir(), "data.json")
	if err := WriteJSON(file, M{"hello": "世界", "n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(file, M{"hello": "changed"}); err != nil {
		t.Fatal(err)
	}
	if ReadMap(file)["hello"] != "changed" {
		t.Fatal("replace failed")
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0600 {
		t.Fatal("permissions")
	}
	jsonl := filepath.Join(t.TempDir(), "points.jsonl")
	os.WriteFile(jsonl, []byte("bad\n{\"product\":\"trae\"}\n[]\n"), 0600)
	if len(ReadJSONL(jsonl)) != 1 {
		t.Fatal("invalid history rows not skipped")
	}
}

func TestIntegerPrecisionAndPythonRounding(t *testing.T) {
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"data":{"id":9007199254740993}}`)) + ".sig"
	if got := String(Map(JWTPayload(token)["data"])["id"]); got != "9007199254740993" {
		t.Fatalf("account ID lost precision: %s", got)
	}
	for _, tc := range []struct{ input, want float64 }{{2.675, 2.67}, {2.685, 2.69}, {0.125, 0.12}} {
		if got := Round(tc.input, 2); got != tc.want {
			t.Fatalf("round(%g, 2) = %g, want %g", tc.input, got, tc.want)
		}
	}
	var m M
	if DecodeJSON([]byte(`{} {}`), &m) == nil {
		t.Fatal("trailing JSON accepted")
	}
}
