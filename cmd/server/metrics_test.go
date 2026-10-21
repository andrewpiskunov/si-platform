package main

import (
	"context"
	"net"
	"net/http"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/telemetry"
	"siplatform/internal/testutil"
	"strings"
	"testing"
	"time"
)

func TestMetricsConfigAndListener(t *testing.T) {
	for _, v := range []struct {
		addr, token string
		ok          bool
	}{{"", "", true}, {"127.0.0.1:9090", "short", false}, {"not-address", strings.Repeat("x", 32), false}, {"127.0.0.1:0", strings.Repeat("x", 32), true}} {
		_, e := metricsOptions(func(k string) string {
			if k == "METRICS_ADDR" {
				return v.addr
			}
			return v.token
		})
		if (e == nil) != v.ok {
			t.Fatal(v.addr, e)
		}
	}
	p := testutil.Pool(t)
	ctx := context.Background()
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	m := telemetry.New()
	runtime, e := startMetrics(ctx, m, p, metricsConfig{Address: "127.0.0.1:0", Token: strings.Repeat("x", 32)})
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	req, _ := http.NewRequest("GET", "http://"+runtime.Address()+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
	client := &http.Client{Timeout: time.Second}
	resp, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	if duplicate, e := startMetrics(ctx, telemetry.New(), p, metricsConfig{Address: runtime.Address(), Token: strings.Repeat("x", 32)}); e == nil {
		duplicate.Close()
		t.Fatal("occupied listener accepted")
	}
	addr := runtime.Address()
	runtime.Close()
	runtime.Close()
	conn, e := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if e == nil {
		conn.Close()
		t.Fatal("listener survived shutdown")
	}
}
