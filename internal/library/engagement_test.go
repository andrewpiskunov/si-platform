package library

import (
	"errors"
	"siplatform/internal/accounts"
	"sync"
	"testing"
)

func ptr[T any](v T) *T { return &v }
func TestEngagementVotesAndBaselines(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	b := identity(t, p, "voter", "author", "session")
	d := create(t, s, a, "Pack")
	if e := s.SetVote(ctx, b, d.Card.ID, Vote{Rating: ptr(5), Liked: true}); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.SetVote(ctx, accounts.Identity{}, d.Card.ID, Vote{Rating: ptr(5)}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	token := credential(t, p, b.User.ID, "api", "drafts:write")
	if e := s.SetVote(ctx, token, d.Card.ID, Vote{Rating: ptr(5)}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e := s.SetVote(ctx, b, d.Card.ID, Vote{Rating: ptr(6)}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.SetVote(ctx, b, d.Card.ID, Vote{Rating: ptr(5), Liked: true}); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	g, e := s.Engagement(ctx, d.Card.ID)
	if e != nil || g.LocalRatingCount != 1 || g.LocalLikes != 1 || *g.Rating != 5 {
		t.Fatal(g, e)
	}
	if e = s.SetVote(ctx, b, d.Card.ID, Vote{Rating: ptr(3), Liked: false}); e != nil {
		t.Fatal(e)
	}
	if e = s.RecordDownload(ctx, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	raw := `{"downloads":300,"rating":4.5,"rating_count":10,"likes":12}`
	for i := 0; i < 2; i++ {
		if _, e = p.Exec(ctx, `INSERT INTO source_metadata(pack_id,external_id,snapshot,summary,search_text) VALUES($1,'1',$2,'{}','') ON CONFLICT(pack_id) DO UPDATE SET snapshot=EXCLUDED.snapshot`, d.Card.ID, raw); e != nil {
			t.Fatal(e)
		}
	}
	g, e = s.Engagement(ctx, d.Card.ID)
	if e != nil || g.Downloads != 301 || g.Likes != 12 || g.RatingCount != 11 || *g.Rating != 48.0/11 {
		t.Fatal(g, e)
	}
	c, e := s.Public(ctx, d.Card.ID)
	if e != nil || c.Engagement == nil || c.Engagement.Downloads != 301 {
		t.Fatal(c, e)
	}
	if _, e = p.Exec(ctx, `UPDATE source_metadata SET snapshot=snapshot-'rating_count'`); e != nil {
		t.Fatal(e)
	}
	g, e = s.Engagement(ctx, d.Card.ID)
	if e != nil || *g.Rating != 3 || g.RatingCount != 1 || g.SourceRatingCount != nil || *g.SourceRating != 4.5 {
		t.Fatal(g, e)
	}
	own, e := s.MyVote(ctx, b, d.Card.ID)
	if e != nil || *own.Rating != 3 || own.Liked {
		t.Fatal(own, e)
	}
	if _, e = p.Exec(ctx, `UPDATE account_users SET deleted_at=now(),username=NULL,password_hash=NULL WHERE id=$1`, b.User.ID); e != nil {
		t.Fatal(e)
	}
	g, e = s.Engagement(ctx, d.Card.ID)
	if e != nil || g.LocalRatingCount != 0 || g.Rating != nil {
		t.Fatal(g, e)
	}
	if _, e = p.Exec(ctx, `UPDATE library_packs SET published_revision=NULL WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Engagement(ctx, d.Card.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e = s.RecordDownload(ctx, d.Card.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
