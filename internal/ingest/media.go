package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"siplatform/internal/accounts"
	"siplatform/internal/library"
	"siplatform/internal/media"
)

func mediaFolder(kind string) string {
	return map[string]string{"image": "Images/", "audio": "Audio/", "video": "Video/"}[kind]
}
func archiveKind(name string) string {
	switch {
	case strings.HasPrefix(name, "images/"):
		return "image"
	case strings.HasPrefix(name, "audio/"):
		return "audio"
	case strings.HasPrefix(name, "video/"):
		return "video"
	}
	return ""
}

func mediaIssueMessage(code string) string {
	switch code {
	case "media_pending":
		return "Медиа ожидает проверки и подготовки для воспроизведения"
	case "media_unavailable":
		return "Подготовка медиа временно недоступна"
	case "media_too_large":
		return "Размер или разрешение медиа превышает допустимый предел"
	case "media_too_long":
		return "Длительность медиа превышает десять минут"
	case "media_timeout":
		return "Подготовка медиа превысила допустимое время"
	default:
		return "Медиа повреждено или имеет неподдерживаемый формат"
	}
}

func (s *Service) processMedia(ctx context.Context, j job, epoch int64) (*library.Asset, string) {
	if _, err := s.liveActor(ctx, j); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
			return nil, "authorization_expired"
		}
		return nil, "storage_unavailable"
	}
	if s.media == nil {
		return nil, "media_pending"
	}
	file, err := s.openFile(j.ID, os.O_RDONLY)
	if err != nil {
		return nil, "staging_unavailable"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != j.Size {
		return nil, "staging_unavailable"
	}
	hash := sha256.New()
	if _, err = io.CopyBuffer(hash, contextReader{ctx, file}, make([]byte, 32<<10)); err != nil {
		return nil, importError(ctx, "staging_unavailable")
	}
	key := hex.EncodeToString(hash.Sum(nil))
	var head [512]byte
	n, err := file.ReadAt(head[:], 0)
	if err != nil && err != io.EOF {
		return nil, "staging_unavailable"
	}
	asset := library.Asset{Name: mediaFolder(j.kind) + j.ID + "-" + j.name, Key: key, ContentType: safeContentType(head[:n]), Size: j.Size}
	if err = s.reserveObjects(ctx, j.ID, epoch, []string{key}); err != nil {
		return nil, importError(ctx, "storage_unavailable")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, "staging_unavailable"
	}
	prepared, code := s.prepareAsset(ctx, j, epoch, asset, j.kind, file)
	if code != "" {
		return nil, code
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, "staging_unavailable"
	}
	if err = s.objects.Put(ctx, key, contextReader{ctx, file}, j.Size, prepared.ContentType); err != nil {
		return nil, importError(ctx, "storage_unavailable")
	}
	return prepared, ""
}

// The same processor and reservation-before-Put path serves SIQ and standalone
// uploads. Files remain private to the processor until verified blob publication.
func (s *Service) prepareAsset(ctx context.Context, j job, epoch int64, asset library.Asset, kind string, source io.Reader) (*library.Asset, string) {
	if s.media == nil {
		return nil, "media_pending"
	}
	result, err := s.media.Prepare(ctx, kind, source, asset.Size)
	if err != nil {
		return nil, "media_" + media.Code(err)
	}
	if result == nil {
		return nil, "media_invalid"
	}
	defer result.Close()
	expected := map[string]media.Variant{result.Prepared.Standard.Key: result.Prepared.Standard, result.Prepared.Economy.Key: result.Prepared.Economy}
	if len(result.Files) < 1 || len(result.Files) > 2 {
		return nil, "media_invalid"
	}
	keys := make([]string, 0, len(result.Files))
	found := map[string]bool{}
	for _, output := range result.Files {
		variant, ok := expected[output.Key]
		if !ok || output.Size != variant.Size || output.MIME != variant.MIME || output.Size <= 0 || output.Size > 100<<20 {
			return nil, "media_invalid"
		}
		keys = append(keys, output.Key)
		found[output.Key] = true
	}
	for key := range expected {
		if !found[key] {
			return nil, "media_invalid"
		}
	}
	if err = s.reserveObjects(ctx, j.ID, epoch, keys); err != nil {
		return nil, importError(ctx, "storage_unavailable")
	}
	for _, output := range result.Files {
		file, err := os.Open(output.Path)
		if err != nil {
			return nil, "media_unavailable"
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != output.Size {
			_ = file.Close()
			return nil, "media_invalid"
		}
		err = s.objects.Put(ctx, output.Key, contextReader{ctx, file}, output.Size, output.MIME)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return nil, importError(ctx, "storage_unavailable")
		}
	}
	prepared := result.Prepared
	asset.Prepared = &prepared
	// Sniffing MP4 identifies the container. The isolated processor verifies
	// whether its streams are audio-only before we specialize the source MIME.
	if prepared.Kind == "audio" && asset.ContentType == "video/mp4" {
		asset.ContentType = "audio/mp4"
	}
	return &asset, ""
}

func (s *Service) finish(ctx context.Context, j job, epoch int64, draftID string, asset *library.Asset, code string) {
	status := "failed"
	if code == "" {
		status = "ready"
	}
	var resultAsset any
	if asset != nil {
		encoded, err := json.Marshal(asset)
		if err != nil {
			status = "failed"
			code = "import_failed"
		} else {
			resultAsset = encoded
		}
	}
	const query = `UPDATE uploads SET status=$3,draft_id=$4,error=$5,result_asset=$6,updated_at=now()
 WHERE id=$1 AND lease_epoch=$2 AND status='processing' AND EXISTS(SELECT 1 FROM ingest_owner WHERE singleton=true AND epoch=$2)`
	if j.Purpose == "media" && status == "ready" {
		// Recheck the originating credential under the same account lock as revocation.
		actor := accounts.Identity{User: accounts.User{ID: j.owner}, SessionID: j.credential}
		tx, err := s.begin(ctx, actor, true)
		if err == nil {
			defer tx.Rollback(context.Background())
			_, storageLimit, limitErr := library.AccountLimits(ctx, tx, j.owner)
			if limitErr != nil {
				return
			}
			used, usageErr := library.StorageUsage(ctx, tx, j.owner)
			if usageErr != nil {
				return
			}
			if asset == nil || asset.Prepared == nil {
				status = "failed"
				code = "media_invalid"
				resultAsset = nil
			} else if used+asset.Size+asset.Prepared.Standard.Size+asset.Prepared.Economy.Size > storageLimit {
				status = "failed"
				code = "draft_quota"
				resultAsset = nil
			}
			result, err := tx.Exec(ctx, query, j.ID, epoch, status, draftID, code, resultAsset)
			if err == nil && tx.Commit(ctx) == nil && result.RowsAffected() == 1 {
				s.removeStage(ctx, j.ID)
			}
			return
		}
		if !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrForbidden) {
			return
		}
		status = "failed"
		code = "authorization_expired"
		resultAsset = nil
	}
	result, err := s.pool.Exec(ctx, query, j.ID, epoch, status, draftID, code, resultAsset)
	if err == nil && result.RowsAffected() == 1 {
		s.removeStage(ctx, j.ID)
	}
}
