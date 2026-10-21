package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/aiformat"
)

// CreateAIDraft accepts an author's reviewed suggestion into a new private pack.
// Account, job and pack locks serialize retries and coordinate deletion/revocation.
func (s *Service) CreateAIDraft(ctx context.Context, actor accounts.Identity, jobID string, reviewed bool, result aiformat.Result) (Draft, error) {
	a, err := s.begin(ctx, actor, "drafts:write", false, "")
	if err != nil {
		return Draft{}, err
	}
	defer a.close()
	if a.actor.APIToken {
		allowed := false
		for _, scope := range a.actor.Scopes {
			if scope == "ai:generate" {
				allowed = true
			}
		}
		if !allowed {
			return Draft{}, ErrForbidden
		}
	}
	if !reviewed || !hexID(jobID, 32) {
		return Draft{}, ErrInvalid
	}
	var status, model, appliedHash string
	var appliedID *string
	var requestJSON []byte
	var generatedAt time.Time
	err = a.tx.QueryRow(ctx, `SELECT status,request,model,COALESCE(completed_at,created_at),applied_pack_id,applied_hash FROM ai_jobs WHERE id=$1 AND owner_id=$2 FOR UPDATE`, jobID, a.actor.User.ID).Scan(&status, &requestJSON, &model, &generatedAt, &appliedID, &appliedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	if status != "ready" {
		return Draft{}, ErrConflict
	}
	var request aiformat.Request
	if err = json.Unmarshal(requestJSON, &request); err != nil {
		return Draft{}, ErrInvalid
	}
	if err = aiformat.ValidateRequest(request); err != nil {
		return Draft{}, ErrInvalid
	}
	if err = aiformat.ValidateResult(request, result); err != nil {
		return Draft{}, ErrInvalid
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return Draft{}, ErrInvalid
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	if appliedID != nil {
		if hash != appliedHash {
			return Draft{}, ErrConflict
		}
		var revision int
		var blocked bool
		err = a.tx.QueryRow(ctx, `SELECT current_revision,blocked FROM library_packs WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL FOR UPDATE`, *appliedID, a.actor.User.ID).Scan(&revision, &blocked)
		if errors.Is(err, pgx.ErrNoRows) {
			return Draft{}, ErrNotFound
		}
		if err != nil {
			return Draft{}, err
		}
		if blocked {
			return Draft{}, ErrBlocked
		}
		return load(ctx, a.tx, *appliedID, revision)
	}
	// Do not let a pre-existing import ID become an unverified AI receipt.
	var exists bool
	if err = a.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM library_packs WHERE owner_id=$1 AND import_id=$2)`, a.actor.User.ID, jobID).Scan(&exists); err != nil {
		return Draft{}, err
	}
	if exists {
		return Draft{}, ErrConflict
	}
	d, err := createInAccess(ctx, a, Save{Document: aiformat.Document(result, jobID, model, generatedAt), ImportID: jobID})
	if err != nil {
		return Draft{}, err
	}
	if _, err = a.tx.Exec(ctx, `UPDATE ai_jobs SET applied_pack_id=$2,applied_hash=$3 WHERE id=$1`, jobID, d.Card.ID, hash); err != nil {
		return Draft{}, err
	}
	if err = a.tx.Commit(ctx); err != nil {
		return Draft{}, err
	}
	return d, nil
}
