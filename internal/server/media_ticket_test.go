package server

import (
	"strings"
	"testing"
	"time"
)

func TestMediaTicketIntegrityAndLifetime(t *testing.T) {
	app := New(nil, Options{})
	defer app.Close()
	now := time.Unix(100000, 0)
	claims := mediaClaims{Room: "room", Participant: "player", Instance: "question-instance", Media: "opaque-media", Quality: "economy", Expires: now.Add(15 * time.Minute).Unix()}
	ticket, err := app.signMediaTicket(claims)
	if err != nil {
		t.Fatal(err)
	}
	got, err := app.readMediaTicket(ticket, now)
	if err != nil || got != claims {
		t.Fatalf("valid ticket: %+v, %v", got, err)
	}
	parts := strings.Split(ticket, ".")
	for name, token := range map[string]string{
		"missing": "", "truncated": parts[0], "tampered_claims": "X" + parts[0] + "." + parts[1],
		"tampered_signature": parts[0] + ".X" + parts[1], "extra": ticket + ".part", "unbounded": strings.Repeat("A", 4096),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := app.readMediaTicket(token, now); err == nil {
				t.Fatal("accepted invalid ticket")
			}
		})
	}
	if _, err := app.readMediaTicket(ticket, now.Add(15*time.Minute)); err == nil {
		t.Fatal("accepted expired ticket")
	}
	if _, err := app.readMediaTicket(ticket, now.Add(-time.Hour)); err == nil {
		t.Fatal("accepted excessive lifetime")
	}
	other := New(nil, Options{})
	defer other.Close()
	if _, err := other.readMediaTicket(ticket, now); err == nil {
		t.Fatal("server restart did not invalidate ticket")
	}
}
