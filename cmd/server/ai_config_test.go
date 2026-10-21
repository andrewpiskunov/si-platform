package main

import (
	"strings"
	"testing"
)

func TestAIConfigurationExplicitAndBounded(t *testing.T) {
	lookup := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	disabled, err := aiOptions(func(key string) string {
		if strings.Contains(key, "KEY") {
			t.Fatal("disabled provider read a secret")
		}
		return ""
	}, "0.0.0.0:8080")
	if err != nil || disabled.Provider != nil {
		t.Fatalf("default generation enabled: %v", err)
	}
	for _, addr := range []string{"0.0.0.0:8080", ":8080", "example.org:8080", "localhost:8080"} {
		if _, err = aiOptions(lookup(map[string]string{"AI_PROVIDER": "fixture"}), addr); err == nil {
			t.Errorf("public/ambiguous fixture address accepted: %s", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:8080", "[::1]:8080"} {
		cfg, e := aiOptions(lookup(map[string]string{"AI_PROVIDER": "fixture"}), addr)
		if e != nil || cfg.Provider == nil || cfg.ProviderLabel != "fixture" {
			t.Fatalf("fixture unavailable: %+v %v", cfg, e)
		}
	}
	valid := map[string]string{"AI_PROVIDER": "openai", "AI_API_KEY": "private-secret", "AI_MODEL": "configured-model", "AI_INPUT_MICRO_USD_PER_MILLION": "1000000", "AI_OUTPUT_MICRO_USD_PER_MILLION": "2000000", "AI_MONTHLY_MICRO_USD": "10000000"}
	cfg, err := aiOptions(lookup(valid), "127.0.0.1:8080")
	if err != nil || cfg.Provider == nil || cfg.Model != "configured-model" || cfg.MaxOutputTokens != 16384 {
		t.Fatalf("valid config rejected: %v", err)
	}
	for _, key := range []string{"AI_API_KEY", "AI_MODEL", "AI_INPUT_MICRO_USD_PER_MILLION", "AI_OUTPUT_MICRO_USD_PER_MILLION", "AI_MONTHLY_MICRO_USD"} {
		copy := map[string]string{}
		for k, v := range valid {
			copy[k] = v
		}
		delete(copy, key)
		_, e := aiOptions(lookup(copy), "127.0.0.1:8080")
		if e == nil || strings.Contains(e.Error(), "private-secret") {
			t.Errorf("missing %s accepted or secret disclosed: %v", key, e)
		}
	}
	for _, value := range []string{"-1", "0", "1.5", "1000000000001", "999999999999999999999999"} {
		copy := map[string]string{}
		for k, v := range valid {
			copy[k] = v
		}
		copy["AI_MONTHLY_MICRO_USD"] = value
		if _, e := aiOptions(lookup(copy), "127.0.0.1:8080"); e == nil {
			t.Errorf("invalid budget accepted: %s", value)
		}
	}
	for _, value := range []string{"255", "64001", "-1"} {
		copy := map[string]string{}
		for k, v := range valid {
			copy[k] = v
		}
		copy["AI_MAX_OUTPUT_TOKENS"] = value
		if _, e := aiOptions(lookup(copy), "127.0.0.1:8080"); e == nil {
			t.Errorf("invalid output ceiling accepted: %s", value)
		}
	}
}
