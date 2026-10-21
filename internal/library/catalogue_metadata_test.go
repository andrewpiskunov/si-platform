package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"siplatform/internal/packs"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"siplatform/migrations"
)

func TestCatalogueThemeSearchAndAge(t *testing.T) {
	s, pool := setup(t)
	owner := identity(t, pool, "owner", "author", "session")
	mod := identity(t, pool, "moderator", "moderator", "session")
	labels := []string{"0+", "6+", "\t12+\r\n", "16+", "18+", "", "для всех", "\u00a06+"}
	drafts := make([]Draft, len(labels))
	for i, label := range labels {
		doc := document(fmt.Sprintf("Collection %d", i))
		doc.Restriction = label
		doc.Info.Authors = []string{"Петров"}
		doc.Rounds[0].Themes[0].Name = `Космос 100%_\`
		var err error
		drafts[i], err = s.Create(ctx, owner, Save{Document: doc})
		if err != nil {
			t.Fatal(err)
		}
		publish(t, s, owner, mod, drafts[i], true)
	}
	check := func(filter Filter, indexes ...int) {
		t.Helper()
		cards, err := s.Catalogue(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		got, want := []string{}, []string{}
		for _, card := range cards {
			got = append(got, card.ID)
		}
		for _, index := range indexes {
			want = append(want, drafts[index].Card.ID)
		}
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("filter %+v: got %v want %v", filter, got, want)
		}
	}
	for _, q := range []string{"кОСмОС", "%_\\", "ПЕТРОВ", "Collection"} {
		check(Filter{Query: q}, 0, 1, 2, 3, 4, 5, 6, 7)
	}
	check(Filter{Query: "secret answer"})
	for i, age := range []string{"0", "6", "12", "16", "18"} {
		indexes := []int{}
		for n := 0; n <= i; n++ {
			indexes = append(indexes, n)
		}
		check(Filter{Age: age}, indexes...)
	}
	check(Filter{Age: "unrated"}, 5, 6, 7)
	difficulty, media := 3, false
	check(Filter{Query: "космос", Age: "6", Language: "ru", Tag: "science", Difficulty: &difficulty, HasMedia: &media, MaxBytes: 1 << 20}, 0, 1)
	check(Filter{Age: "6", Language: "en"})
	for _, age := range []string{"12+", "7", "-1", " 6", "6 ", "UNRATED", "6 OR true"} {
		if _, err := s.Catalogue(ctx, Filter{Age: age}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("age %q: %v", age, err)
		}
	}
	// A literal wildcard must not match an otherwise unrelated public package.
	plain := create(t, s, owner, "Plain")
	publish(t, s, owner, mod, plain, true)
	check(Filter{Query: "%_\\"}, 0, 1, 2, 3, 4, 5, 6, 7)
	updated := drafts[0].Document
	updated.Rounds[0].Themes[0].Name = "Закрытая новинка"
	if _, err := s.Update(ctx, owner, drafts[0].Card.ID, 1, Save{Document: updated}); err != nil {
		t.Fatal(err)
	}
	check(Filter{Query: "Закрытая новинка"})
	check(Filter{Query: "космос", Age: "0"}, 0)
	privateDoc := document("Private")
	privateDoc.Rounds[0].Themes[0].Name = "Непроверенная тема"
	private, err := s.Create(ctx, owner, Save{Document: privateDoc})
	if err != nil {
		t.Fatal(err)
	}
	check(Filter{Query: "Непроверенная тема"})
	if _, err = s.Submit(ctx, owner, private.Card.ID, 1, rights(true)); err != nil {
		t.Fatal(err)
	}
	check(Filter{Query: "Непроверенная тема"})
	if _, err = s.Decide(ctx, mod, private.Card.ID, 1, "reject", "Needs sources"); err != nil {
		t.Fatal(err)
	}
	check(Filter{Query: "Непроверенная тема"})
	if err = s.Withdraw(ctx, owner, drafts[0].Card.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Decide(ctx, mod, drafts[1].Card.ID, 1, "block", "Rights issue"); err != nil {
		t.Fatal(err)
	}
	check(Filter{Query: "космос"}, 2, 3, 4, 5, 6, 7)
}

func TestCatalogueMetadataBackfill(t *testing.T) {
	pool := testutil.Pool(t)
	db := stdlib.OpenDB(*pool.Config().ConnConfig)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	identity(t, pool, "old_owner", "author", "session")
	documents := []string{
		`{"rounds":[{"themes":[{"name":"Космос"},{"name":"100%_\\"}]},{"themes":null}]}`,
		`{"rounds":null}`, `{}`, `{"rounds":[{"themes":null},{}]}`,
	}
	for i, raw := range documents {
		card := Card{Name: "Old", Author: "Автор", Restriction: "\t12+\r\n"}
		cardJSON, _ := json.Marshal(card)
		id := fmt.Sprintf("old-%d", i)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO library_packs(id,owner_id,current_revision) VALUES($1,'old_owner',1)`, id)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO library_revisions(pack_id,revision,card,document,assets,issues,size_bytes) VALUES($1,1,$2,$3,'[]','[]',0)`, id, cardJSON, raw)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal("repeat", err)
	}
	for i, raw := range documents {
		var doc packs.Document
		if err = json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		wantSearch, wantAge := catalogueMetadata(Card{Name: "Old", Author: "Автор", Restriction: "\t12+\r\n"}, doc)
		var search string
		var age *int
		if err = pool.QueryRow(ctx, `SELECT search_text,age_rating FROM library_revisions WHERE pack_id=$1`, fmt.Sprintf("old-%d", i)).Scan(&search, &age); err != nil {
			t.Fatal(err)
		}
		if search != wantSearch || !reflect.DeepEqual(age, wantAge) {
			t.Fatalf("backfill %d: %q/%v want %q/%v", i, search, age, wantSearch, wantAge)
		}
	}
	// Rollback must not remove the database-wide extension used by other schemas.
	if _, err = provider.DownTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	var installed bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_trgm')`).Scan(&installed); err != nil || !installed {
		t.Fatal("shared extension removed", err)
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal("reapply", err)
	}
}
