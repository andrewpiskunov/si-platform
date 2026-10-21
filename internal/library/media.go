package library

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/game"
	"siplatform/internal/media"
	"siplatform/internal/playpack"
)

func preparedSize(asset Asset) (int64, error) {
	p := asset.Prepared
	if p == nil {
		return 0, nil
	}
	if asset.Size <= 0 || asset.Size > 100<<20 {
		return 0, ErrInvalid
	}
	expectedMIME := ""
	folder := ""
	switch p.Kind {
	case "image":
		expectedMIME = "image/png"
		folder = "images/"
		if p.DurationMS != 0 || p.Width <= 0 || p.Height <= 0 || int64(p.Width) > 16000000/int64(p.Height) {
			return 0, ErrInvalid
		}
	case "audio":
		expectedMIME = "audio/mp4"
		folder = "audio/"
		if p.DurationMS < 1 || p.DurationMS > 600000 || p.Width != 0 || p.Height != 0 {
			return 0, ErrInvalid
		}
	case "video":
		expectedMIME = "video/mp4"
		folder = "video/"
		if p.DurationMS < 1 || p.DurationMS > 600000 || p.Width <= 0 || p.Width > 3840 || p.Height <= 0 || p.Height > 2160 {
			return 0, ErrInvalid
		}
	default:
		return 0, ErrInvalid
	}
	name, err := assetName(asset.Name)
	if err != nil || !strings.HasPrefix(name, folder) {
		return 0, ErrInvalid
	}
	var total int64
	for _, variant := range []media.Variant{p.Standard, p.Economy} {
		if !hexID(variant.Key, 64) || variant.MIME != expectedMIME || variant.Size <= 0 || variant.Size > 100<<20 {
			return 0, ErrInvalid
		}
		total += variant.Size
	}
	return total, nil
}

// MediaLookup resolves SIQ references against private immutable prepared assets.
// Only the game compiler assigns the opaque public content ID.
func MediaLookup(assets []Asset) playpack.AssetLookup {
	indexed := make(map[string]Asset, len(assets))
	ambiguous := make(map[string]bool)
	for _, asset := range assets {
		name, err := assetName(asset.Name)
		if err != nil {
			continue
		}
		if _, ok := indexed[name]; ok {
			ambiguous[name] = true
		}
		indexed[name] = asset
	}
	return func(kind, reference string) (game.MediaSpec, error) {
		folder := map[string]string{"image": "Images/", "audio": "Audio/", "video": "Video/"}[kind]
		if folder == "" {
			return game.MediaSpec{}, ErrInvalid
		}
		name, err := assetName(folder + reference)
		if err != nil || ambiguous[name] {
			return game.MediaSpec{}, ErrInvalid
		}
		asset, ok := indexed[name]
		if !ok || asset.Prepared == nil {
			return game.MediaSpec{}, ErrNotFound
		}
		if _, err = preparedSize(asset); err != nil || asset.Prepared.Kind != kind {
			return game.MediaSpec{}, ErrInvalid
		}
		p := asset.Prepared
		return game.MediaSpec{Kind: p.Kind, DurationMS: p.DurationMS, Width: p.Width, Height: p.Height,
			Standard: game.MediaVariant{Key: p.Standard.Key, MIME: p.Standard.MIME, Size: p.Standard.Size},
			Economy:  game.MediaVariant{Key: p.Economy.Key, MIME: p.Economy.MIME, Size: p.Economy.Size}}, nil
	}
}

// AttachAsset accepts a trusted ingest result and verifies it against the durable
// upload. Authorization, optimistic revision, quota and receipt commit together.
func (s *Service) AttachAsset(ctx context.Context, actor accounts.Identity, packID string, baseRevision int, uploadID string, asset Asset) (Draft, error) {
	a, err := s.begin(ctx, actor, "drafts:write", false, packID)
	if err != nil {
		return Draft{}, err
	}
	defer a.close()
	if err = a.owner(); err != nil {
		return Draft{}, err
	}
	if a.pack.blocked {
		return Draft{}, ErrBlocked
	}
	if !hexID(uploadID, 32) {
		return Draft{}, ErrInvalid
	}
	var applied bool
	if err = a.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM library_media_receipts WHERE pack_id=$1 AND upload_id=$2)`, packID, uploadID).Scan(&applied); err != nil {
		return Draft{}, err
	}
	if applied {
		return load(ctx, a.tx, packID, a.pack.current)
	}
	if baseRevision != a.pack.current {
		return Draft{}, ErrConflict
	}
	if baseRevision >= 500 {
		return Draft{}, ErrQuota
	}
	var raw []byte
	err = a.tx.QueryRow(ctx, `SELECT result_asset FROM uploads WHERE id=$1 AND owner_id=$2 AND purpose='media' AND status='ready'`, uploadID, a.actor.User.ID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	var stored Asset
	if json.Unmarshal(raw, &stored) != nil || stored.Prepared == nil || !reflect.DeepEqual(stored, asset) {
		return Draft{}, ErrInvalid
	}
	prior, err := load(ctx, a.tx, packID, a.pack.current)
	if err != nil {
		return Draft{}, err
	}
	assets := append(prior.Assets, stored)
	d, err := prepare(ctx, Save{Document: prior.Document, Assets: assets, OriginalKey: prior.OriginalKey, Issues: prior.AssetIssues})
	if err != nil {
		return Draft{}, err
	}
	used, err := usage(ctx, a.tx, a.actor.User.ID)
	if err != nil {
		return Draft{}, err
	}
	var previouslyAttached bool
	err = a.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM library_media_receipts receipt JOIN library_packs p ON p.id=receipt.pack_id WHERE receipt.upload_id=$1 AND p.deleted_at IS NULL)`, uploadID).Scan(&previouslyAttached)
	if err != nil {
		return Draft{}, err
	}
	if !previouslyAttached {
		// The first receipt transfers ready-upload bytes into revision storage.
		// Subsequent packs/revisions pay their full additional logical size.
		used -= stored.Size + stored.Prepared.Standard.Size + stored.Prepared.Economy.Size
	}
	if used+d.Card.SizeBytes > 10<<30 {
		return Draft{}, ErrQuota
	}
	d.Card.ID = packID
	d.Card.OwnerID = a.actor.User.ID
	d.Card.Revision = baseRevision + 1
	if d.Card.Author == "" {
		d.Card.Author = a.actor.User.DisplayName
	}
	if err = insert(ctx, a.tx, d); err != nil {
		return Draft{}, err
	}
	if _, err = a.tx.Exec(ctx, `UPDATE library_packs SET current_revision=$2 WHERE id=$1`, packID, d.Card.Revision); err != nil {
		return Draft{}, err
	}
	if _, err = a.tx.Exec(ctx, `INSERT INTO library_media_receipts(pack_id,upload_id,revision) VALUES($1,$2,$3)`, packID, uploadID, d.Card.Revision); err != nil {
		return Draft{}, err
	}
	result, err := load(ctx, a.tx, packID, d.Card.Revision)
	if err != nil {
		return Draft{}, err
	}
	if err = a.tx.Commit(ctx); err != nil {
		return Draft{}, err
	}
	return result, nil
}
