package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSampleValidateConvert(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "demo.siq")
	output := filepath.Join(dir, "converted.siq")
	var out bytes.Buffer
	for _, args := range [][]string{{"sample", input}, {"validate", input}, {"convert", input, output}, {"validate", output}} {
		out.Reset()
		if err := run(args, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if !bytes.Contains(out.Bytes(), []byte(`"playable": true`)) {
		t.Fatalf("report: %s", out.String())
	}
	before, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if err = run([]string{"convert", output, input}, &out); err == nil {
		t.Fatal("existing file overwritten")
	}
	after, _ := os.ReadFile(input)
	if !bytes.Equal(before, after) {
		t.Fatal("source modified")
	}
}
