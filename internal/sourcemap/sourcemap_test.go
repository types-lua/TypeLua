package sourcemap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVLQVectors(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "A"},
		{1, "C"},
		{2, "E"},
		{-1, "D"},
		{-2, "F"},
		{16, "gB"},
		{1000, "w+B"},
	}
	for _, c := range cases {
		if got := string(AppendVLQ(nil, c.in)); got != c.want {
			t.Fatalf("VLQ(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func decodeVLQ(s string) []int {
	var out []int
	i := 0
	for i < len(s) {
		shift := uint(0)
		var v uint32
		for {
			d := strings.IndexByte(base64Chars, s[i])
			i++
			v |= uint32(d&31) << shift
			shift += 5
			if d&32 == 0 {
				break
			}
		}
		if v&1 == 1 {
			out = append(out, -int(v>>1))
		} else {
			out = append(out, int(v>>1))
		}
	}
	return out
}

func TestVLQRoundTrip(t *testing.T) {
	for _, v := range []int{-100000, -33, -1, 0, 1, 5, 31, 32, 1000, 1 << 20} {
		got := decodeVLQ(string(AppendVLQ(nil, v)))
		if len(got) != 1 || got[0] != v {
			t.Fatalf("roundtrip(%d) = %v", v, got)
		}
	}
}

func TestEncodeShape(t *testing.T) {
	ms := []Mapping{
		{GenLine: 1, GenCol: 0, SrcFile: "a.tl", SrcLine: 0, SrcCol: 0},
		{GenLine: 1, GenCol: 6, SrcFile: "a.tl", SrcLine: 0, SrcCol: 6, Name: "x"},
		{GenLine: 3, GenCol: 4, SrcFile: "b.tl", SrcLine: 5, SrcCol: 2},
	}
	raw, err := Encode("a.lua", []string{"a.tl"}, []string{"src"}, nil, ms)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["version"].(float64) != 3 {
		t.Fatalf("version: %v", doc["version"])
	}
	if doc["mappings"].(string) == "" {
		t.Fatalf("empty mappings")
	}
	names := doc["names"].([]any)
	if len(names) != 1 || names[0].(string) != "x" {
		t.Fatalf("names: %v", names)
	}
}
