package main

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"siplatform/internal/ai"
	"siplatform/internal/aiprovider"
)

func aiOptions(getenv func(string) string, address string) (ai.Options, error) {
	invalid := errors.New("AI configuration invalid: select disabled, openai with explicit model/key/rates/budget, or fixture on a numeric loopback address")
	var out ai.Options
	switch getenv("AI_PROVIDER") {
	case "", "disabled":
		return out, nil
	case "fixture":
		host, _, err := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			return out, invalid
		}
		return ai.Options{Provider: aiprovider.NewFixture(), ProviderLabel: "fixture", Model: "fixture-v1", InputRate: 1000000, OutputRate: 1000000, MonthlyLimit: 10000000, MaxOutputTokens: 16384}, nil
	case "openai":
		model, key := getenv("AI_MODEL"), getenv("AI_API_KEY")
		if model == "" || len(model) > 200 || strings.TrimSpace(model) != model || strings.ContainsAny(model, "\r\n\t") || strings.TrimSpace(key) == "" {
			return out, invalid
		}
		positive := func(name string, limit int64) (int64, error) {
			raw := getenv(name)
			for _, ch := range raw {
				if ch < '0' || ch > '9' {
					return 0, invalid
				}
			}
			n, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || n < 1 || n > limit {
				return 0, invalid
			}
			return n, nil
		}
		var err error
		if out.InputRate, err = positive("AI_INPUT_MICRO_USD_PER_MILLION", 1e12); err != nil {
			return ai.Options{}, invalid
		}
		if out.OutputRate, err = positive("AI_OUTPUT_MICRO_USD_PER_MILLION", 1e12); err != nil {
			return ai.Options{}, invalid
		}
		if out.MonthlyLimit, err = positive("AI_MONTHLY_MICRO_USD", 1e12); err != nil {
			return ai.Options{}, invalid
		}
		out.MaxOutputTokens = 16384
		if getenv("AI_MAX_OUTPUT_TOKENS") != "" {
			n, e := positive("AI_MAX_OUTPUT_TOKENS", 64000)
			if e != nil || n < 256 {
				return ai.Options{}, invalid
			}
			out.MaxOutputTokens = int(n)
		}
		out.Model = model
		out.ProviderLabel = "openai"
		out.Provider = aiprovider.New(key)
		return out, nil
	default:
		return out, invalid
	}
}
