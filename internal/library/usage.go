package library

import (
	"context"
	"siplatform/internal/accounts"
)

// These are the existing account capacities, shared by reporting and pack writes.
const (
	StorageLimitBytes int64 = 10 << 30
	PackCountLimit          = 200
	PackRevisionLimit       = 500
)

type StorageQuota struct {
	UsedBytes      int64 `json:"used_bytes"`
	LimitBytes     int64 `json:"limit_bytes"`
	RemainingBytes int64 `json:"remaining_bytes"`
}
type CountQuota struct {
	Used      int `json:"used"`
	Limit     int `json:"limit"`
	Remaining int `json:"remaining"`
}
type UploadQuota struct {
	Active    int `json:"active"`
	Limit     int `json:"limit"`
	Remaining int `json:"remaining"`
}
type UsageLimits struct {
	PackRevisions  int   `json:"pack_revisions"`
	ThemeRevisions int   `json:"theme_revisions"`
	SIQBytes       int64 `json:"siq_bytes"`
	MediaBytes     int64 `json:"media_bytes"`
	ThemeBytes     int64 `json:"theme_bytes"`
}
type Usage struct {
	Storage StorageQuota `json:"storage"`
	Packs   CountQuota   `json:"packs"`
	Themes  CountQuota   `json:"themes"`
	Uploads UploadQuota  `json:"uploads"`
	Limits  UsageLimits  `json:"limits"`
}

// Usage reports charged capacity, not physical object-store deduplication or a
// reservation. It uses the same account lock and accounting as content writes.
func (s *Service) Usage(ctx context.Context, actor accounts.Identity) (Usage, error) {
	a, err := s.begin(ctx, actor, "", true, "")
	if err != nil {
		return Usage{}, err
	}
	defer a.close()
	used, err := StorageUsage(ctx, a.tx, a.actor.User.ID)
	if err != nil {
		return Usage{}, err
	}
	u := Usage{Storage: StorageQuota{UsedBytes: used, LimitBytes: StorageLimitBytes, RemainingBytes: max(0, StorageLimitBytes-used)}, Packs: CountQuota{Limit: PackCountLimit}, Themes: CountQuota{Limit: 100}, Uploads: UploadQuota{Limit: 2}, Limits: UsageLimits{PackRevisions: PackRevisionLimit, ThemeRevisions: 100, SIQBytes: 250 << 20, MediaBytes: 100 << 20, ThemeBytes: 5 << 20}}
	err = a.tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM library_packs WHERE owner_id=$1 AND deleted_at IS NULL),
 (SELECT count(*) FROM themes WHERE owner_id=$1),
 (SELECT count(*) FROM uploads WHERE owner_id=$1 AND status IN ('uploading','queued','processing') AND expires_at>clock_timestamp())`, a.actor.User.ID).Scan(&u.Packs.Used, &u.Themes.Used, &u.Uploads.Active)
	if err != nil {
		return Usage{}, err
	}
	u.Packs.Remaining = max(0, u.Packs.Limit-u.Packs.Used)
	u.Themes.Remaining = max(0, u.Themes.Limit-u.Themes.Used)
	u.Uploads.Remaining = max(0, u.Uploads.Limit-u.Uploads.Active)
	return u, nil
}
