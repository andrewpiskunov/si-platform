package library

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
)

type Vote struct {
	Rating *int `json:"rating"`
	Liked  bool `json:"liked"`
}
type Engagement struct {
	Downloads         int64    `json:"downloads"`
	Likes             int64    `json:"likes"`
	Rating            *float64 `json:"rating"`
	RatingCount       int64    `json:"rating_count"`
	LocalDownloads    int64    `json:"local_downloads"`
	LocalLikes        int64    `json:"local_likes"`
	LocalRating       *float64 `json:"local_rating"`
	LocalRatingCount  int64    `json:"local_rating_count"`
	SourceDownloads   *int64   `json:"source_downloads"`
	SourceLikes       *int64   `json:"source_likes"`
	SourceRating      *float64 `json:"source_rating"`
	SourceRatingCount *int64   `json:"source_rating_count"`
}

// Correlated aggregate is evaluated only for cards selected by the catalogue query.
const engagementColumns = `(SELECT jsonb_build_object(
 'local_downloads',COALESCE((SELECT downloads FROM package_downloads WHERE pack_id=p.id),0),
 'local_likes',count(*) FILTER(WHERE v.liked),
 'local_rating',avg(v.rating),'local_rating_count',count(v.rating),
 'source_downloads',(SELECT snapshot->'downloads' FROM source_metadata WHERE pack_id=p.id),
 'source_likes',(SELECT snapshot->'likes' FROM source_metadata WHERE pack_id=p.id),
 'source_rating',(SELECT snapshot->'rating' FROM source_metadata WHERE pack_id=p.id),
 'source_rating_count',(SELECT snapshot->'rating_count' FROM source_metadata WHERE pack_id=p.id))
 FROM package_votes v JOIN account_users voter ON voter.id=v.user_id AND voter.deleted_at IS NULL WHERE v.pack_id=p.id)`

func decodeEngagement(raw []byte) (*Engagement, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var e Engagement
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	e.Downloads = e.LocalDownloads
	if e.SourceDownloads != nil {
		e.Downloads += *e.SourceDownloads
	}
	e.Likes = e.LocalLikes
	if e.SourceLikes != nil {
		e.Likes += *e.SourceLikes
	}
	e.Rating = e.LocalRating
	e.RatingCount = e.LocalRatingCount
	if e.SourceRating != nil && e.SourceRatingCount != nil && *e.SourceRatingCount > 0 {
		sum := *e.SourceRating * float64(*e.SourceRatingCount)
		if e.LocalRating != nil {
			sum += *e.LocalRating * float64(e.LocalRatingCount)
		}
		e.RatingCount += *e.SourceRatingCount
		avg := sum / float64(e.RatingCount)
		e.Rating = &avg
	}
	return &e, nil
}
func (s *Service) Engagement(ctx context.Context, pack string) (Engagement, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, "SELECT "+engagementColumns+publicJoin+" AND p.id=$1", pack).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Engagement{}, ErrNotFound
	}
	if err != nil {
		return Engagement{}, err
	}
	e, err := decodeEngagement(raw)
	if err != nil {
		return Engagement{}, err
	}
	return *e, nil
}
func (s *Service) MyVote(ctx context.Context, actor accounts.Identity, pack string) (Vote, error) {
	a, err := s.begin(ctx, actor, "", true, pack)
	if err != nil {
		return Vote{}, err
	}
	defer a.close()
	if a.pack.blocked || a.pack.published == nil {
		return Vote{}, ErrNotFound
	}
	var v Vote
	err = a.tx.QueryRow(ctx, `SELECT rating,liked FROM package_votes WHERE pack_id=$1 AND user_id=$2`, pack, actor.User.ID).Scan(&v.Rating, &v.Liked)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return v, err
}
func (s *Service) SetVote(ctx context.Context, actor accounts.Identity, pack string, v Vote) error {
	if v.Rating != nil && (*v.Rating < 1 || *v.Rating > 5) {
		return ErrInvalid
	}
	a, err := s.begin(ctx, actor, "", true, pack)
	if err != nil {
		return err
	}
	defer a.close()
	if a.pack.blocked || a.pack.published == nil {
		return ErrNotFound
	}
	if v.Rating == nil && !v.Liked {
		_, err = a.tx.Exec(ctx, `DELETE FROM package_votes WHERE pack_id=$1 AND user_id=$2`, pack, actor.User.ID)
	} else {
		_, err = a.tx.Exec(ctx, `INSERT INTO package_votes(pack_id,user_id,rating,liked) VALUES($1,$2,$3,$4) ON CONFLICT(pack_id,user_id) DO UPDATE SET rating=EXCLUDED.rating,liked=EXCLUDED.liked,updated_at=clock_timestamp()`, pack, actor.User.ID, v.Rating, v.Liked)
	}
	if err != nil {
		return err
	}
	return a.tx.Commit(ctx)
}

// Called only after a complete public SIQ transfer, never by the importer or draft export.
func (s *Service) RecordDownload(ctx context.Context, pack string) error {
	tag, err := s.pool.Exec(ctx, `INSERT INTO package_downloads(pack_id,downloads) SELECT p.id,1`+publicJoin+` AND p.id=$1 ON CONFLICT(pack_id) DO UPDATE SET downloads=package_downloads.downloads+1`, pack)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
