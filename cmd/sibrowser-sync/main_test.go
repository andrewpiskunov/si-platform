package main

import (
	"context"
	"io"
	"testing"
)

func TestCLIRejectsInvalidInputsBeforeConnecting(t *testing.T) {
	env := func(k string) string {
		switch k {
		case "DATABASE_URL":
			return "postgres://invalid"
		case "SI_API_URL":
			return "https://example.test"
		case "SI_API_TOKEN":
			return "test"
		}
		return ""
	}
	for _, args := range [][]string{{"--mode=wrong"}, {"--max-pages=-1"}, {"--timeout=0s"}, {"--interval=100ms"}, {"--configure-owner=owner"}, {"--configure-owner=owner", "--pack-limit=100001", "--storage-limit-bytes=1000"}} {
		if code := run(context.Background(), args, env, io.Discard, io.Discard); code != 2 {
			t.Fatal(args, code)
		}
	}
	if code := run(context.Background(), nil, func(string) string { return "" }, io.Discard, io.Discard); code != 2 {
		t.Fatal(code)
	}
	if code := run(context.Background(), []string{"--help"}, env, io.Discard, io.Discard); code != 0 {
		t.Fatal(code)
	}
}
