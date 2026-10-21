package util

import (
	"checkin/internal/fileperm"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	if !fileperm.Private(file, 0600) {
		t.Fatal("permissions")
	}
	jsonl := filepath.Join(t.TempDir(), "points.jsonl")
	os.WriteFile(jsonl, []byte("bad\n{\"product\":\"trae\"}\n[]\n"), 0600)
	if len(ReadJSONL(jsonl)) != 1 {
		t.Fatal("invalid history rows not skipped")
	}
}

func TestUnixTimeAndFloatPtr(t *testing.T) {
	date := float64(time.Date(2026, 10, 21, 8, 0, 0, 0, time.Local).Unix())
	rfc, _ := time.Parse(time.RFC3339, "2026-10-21T08:00:00+08:00")
	cases := []struct {
		in   any
		want float64
		nil  bool
	}{
		{1800000000.0, 1800000000, false},
		{"1800000000", 1800000000, false},
		{1800000000000.0, 1800000000, false},
		{"1800000000000", 1800000000, false},
		{"2026-10-21 08:00:00", date, false},
		{"2026-10-21T08:00:00+08:00", float64(rfc.Unix()), false},
		{0.0, 0, true},
		{"", 0, true},
		{"not-a-time", 0, true},
	}
	for _, c := range cases {
		got := UnixTime(c.in)
		if c.nil {
			if got != nil {
				t.Fatalf("UnixTime(%v) = %v, want nil", c.in, *got)
			}
			continue
		}
		if got == nil || *got != c.want {
			t.Fatalf("UnixTime(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if v := FloatPtr("12.5"); v == nil || *v != 12.5 {
		t.Fatalf("FloatPtr string = %v", v)
	}
	if FloatPtr("") != nil || FloatPtr("abc") != nil {
		t.Fatal("FloatPtr accepted invalid")
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
