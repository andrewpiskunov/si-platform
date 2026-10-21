package server

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"siplatform/examples"
	"siplatform/internal/library"
)

func TestCatalogueAgeHTTP(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	session, err := auth.Register(ctx, "catalogue_author", "long-enough-password", "Автор")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	// Provision the moderator through the same durable role model as other HTTP tests.
	if _, err = app.store.Pool().Exec(ctx, `UPDATE account_users SET role='moderator' WHERE id=$1`, actor.User.ID); err != nil {
		t.Fatal(err)
	}
	for i, age := range []string{"6+", "18+", ""} {
		doc := examples.Demo()
		doc.Name = "Catalogue"
		doc.Restriction = age
		doc.Rounds[0].Themes[0].Name = "Планеты"
		draft, err := app.options.Library.Create(ctx, actor, library.Save{Document: doc})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = app.options.Library.Submit(ctx, actor, draft.Card.ID, 1, library.Rights{License: "Own", SourceURL: "original", Attribution: "Автор", Confirmed: true}); err != nil {
			t.Fatal(err)
		}
		if _, err = app.options.Library.Decide(ctx, actor, draft.Card.ID, 1, "approve", ""); err != nil {
			t.Fatal(i, err)
		}
	}
	for _, tc := range []struct {
		age   string
		count int
	}{{"", 3}, {"0", 0}, {"6", 1}, {"12", 1}, {"16", 1}, {"18", 2}, {"unrated", 1}} {
		var result struct {
			Items []library.Card `json:"items"`
		}
		path := h.URL + "/api/catalogue?" + url.Values{"age": {tc.age}, "q": {"ПЛАНЕТЫ"}, "language": {"ru"}, "media": {"false"}}.Encode()
		if err = json.Unmarshal(api(t, "GET", path, "", nil, 200), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != tc.count {
			t.Fatalf("age %q: %d want %d", tc.age, len(result.Items), tc.count)
		}
	}
	for _, age := range []string{"12+", "19", "unrated ", "1 OR TRUE"} {
		api(t, "GET", h.URL+"/api/catalogue?age="+url.QueryEscape(age), "", nil, 400)
	}
}
