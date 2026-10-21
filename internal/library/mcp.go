package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/packs"
)

const MCPReceiptLimit = 100000

func mcpHash(key string, input any) (string, error) {
	if len(key) < 16 || len(key) > 128 {
		return "", ErrInvalid
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return "", ErrInvalid
		}
	}
	b, e := json.Marshal(input)
	if e != nil || len(b) > 2<<20 {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func mcpOwner(a *access) error {
	if !a.actor.APIToken {
		return ErrForbidden
	}
	if e := a.owner(); e != nil {
		return e
	}
	if a.pack.blocked {
		return ErrBlocked
	}
	return nil
}
func (s *Service) MCPOwn(ctx context.Context, actor accounts.Identity, packID string, revision int, scope string) (Draft, error) {
	if scope != "drafts:read" && scope != "drafts:export" {
		return Draft{}, ErrInvalid
	}
	a, e := s.begin(ctx, actor, scope, false, packID)
	if e != nil {
		return Draft{}, e
	}
	defer a.close()
	if e = mcpOwner(a); e != nil {
		return Draft{}, e
	}
	if revision < 0 {
		return Draft{}, ErrInvalid
	}
	if revision == 0 {
		revision = a.pack.current
	}
	return load(ctx, a.tx, packID, revision)
}
func mcpReplay(ctx context.Context, a *access, key, hash string) (Card, bool, error) {
	var stored, packID string
	var revision int
	e := a.tx.QueryRow(ctx, `SELECT request_hash,pack_id,result_revision FROM mcp_receipts WHERE owner_id=$1 AND idempotency_key=$2`, a.actor.User.ID, key).Scan(&stored, &packID, &revision)
	if errors.Is(e, pgx.ErrNoRows) {
		return Card{}, false, nil
	}
	if e != nil {
		return Card{}, true, e
	}
	if stored != hash {
		return Card{}, true, ErrConflict
	}
	var blocked bool
	e = a.tx.QueryRow(ctx, `SELECT blocked FROM library_packs WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL FOR UPDATE`, packID, a.actor.User.ID).Scan(&blocked)
	if errors.Is(e, pgx.ErrNoRows) {
		return Card{}, true, ErrNotFound
	}
	if e != nil {
		return Card{}, true, e
	}
	if blocked {
		return Card{}, true, ErrBlocked
	}
	d, e := load(ctx, a.tx, packID, revision)
	return d.Card, true, e
}
func mcpCapacity(ctx context.Context, a *access) error {
	var n int
	e := a.tx.QueryRow(ctx, `SELECT count(*) FROM mcp_receipts WHERE owner_id=$1`, a.actor.User.ID).Scan(&n)
	if e != nil {
		return e
	}
	if n >= MCPReceiptLimit {
		return ErrQuota
	}
	return nil
}
func mcpRecord(ctx context.Context, a *access, key, operation, hash string, card Card) error {
	_, e := a.tx.Exec(ctx, `INSERT INTO mcp_receipts(owner_id,idempotency_key,operation,request_hash,pack_id,result_revision) VALUES($1,$2,$3,$4,$5,$6)`, a.actor.User.ID, key, operation, hash, card.ID, card.Revision)
	return e
}
func (s *Service) MCPCreate(ctx context.Context, actor accounts.Identity, key string, doc packs.Document) (Card, error) {
	if len(doc.ImportIssues) > 0 {
		return Card{}, ErrInvalid
	}
	hash, e := mcpHash(key, struct {
		Operation string
		Document  packs.Document
	}{"create_draft", doc})
	if e != nil {
		return Card{}, e
	}
	a, e := s.begin(ctx, actor, "drafts:write", false, "")
	if e != nil {
		return Card{}, e
	}
	defer a.close()
	if !a.actor.APIToken {
		return Card{}, ErrForbidden
	}
	if card, found, e := mcpReplay(ctx, a, key, hash); found || e != nil {
		return card, e
	}
	if e = mcpCapacity(ctx, a); e != nil {
		return Card{}, e
	}
	d, e := createInAccess(ctx, a, Save{Document: doc})
	if e != nil {
		return Card{}, e
	}
	if e = mcpRecord(ctx, a, key, "create_draft", hash, d.Card); e != nil {
		return Card{}, e
	}
	if e = a.tx.Commit(ctx); e != nil {
		return Card{}, e
	}
	return d.Card, nil
}
func mcpID(id string) bool {
	if !utf8.ValidString(id) || len(id) > 200 || strings.TrimSpace(id) == "" {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (s *Service) MCPUpsert(ctx context.Context, actor accounts.Identity, key, packID string, base int, roundID, themeID string, questions []packs.Question) (Card, error) {
	if base < 1 || !hexID(packID, 32) || !mcpID(roundID) || !mcpID(themeID) || len(questions) < 1 || len(questions) > 100 {
		return Card{}, ErrInvalid
	}
	replacements := map[string]packs.Question{}
	for _, q := range questions {
		if !mcpID(q.ID) {
			return Card{}, ErrInvalid
		}
		if _, ok := replacements[q.ID]; ok {
			return Card{}, ErrInvalid
		}
		replacements[q.ID] = q
	}
	input := struct {
		Operation, PackID string
		Base              int
		RoundID, ThemeID  string
		Questions         []packs.Question
	}{"upsert_questions", packID, base, roundID, themeID, questions}
	hash, e := mcpHash(key, input)
	if e != nil {
		return Card{}, e
	}
	a, e := s.begin(ctx, actor, "drafts:write", false, packID)
	if e != nil {
		return Card{}, e
	}
	defer a.close()
	if e = mcpOwner(a); e != nil {
		return Card{}, e
	}
	if card, found, e := mcpReplay(ctx, a, key, hash); found || e != nil {
		return card, e
	}
	if base != a.pack.current {
		return Card{}, ErrConflict
	}
	if e = mcpCapacity(ctx, a); e != nil {
		return Card{}, e
	}
	prior, e := load(ctx, a.tx, packID, base)
	if e != nil {
		return Card{}, e
	}
	ri, ti := -1, -1
	seen := map[string]bool{}
	for r, round := range prior.Document.Rounds {
		for t, theme := range round.Themes {
			target := round.ID == roundID && theme.ID == themeID
			if target {
				if ri != -1 {
					return Card{}, ErrInvalid
				}
				ri, ti = r, t
			}
			for _, q := range theme.Questions {
				if seen[q.ID] {
					return Card{}, ErrInvalid
				}
				seen[q.ID] = true
				if _, ok := replacements[q.ID]; ok && !target {
					return Card{}, ErrInvalid
				}
			}
		}
	}
	if ri < 0 {
		return Card{}, ErrNotFound
	}
	theme := &prior.Document.Rounds[ri].Themes[ti]
	for i, q := range theme.Questions {
		if replacement, ok := replacements[q.ID]; ok {
			theme.Questions[i] = replacement
		}
	}
	for _, q := range questions {
		if !seen[q.ID] {
			theme.Questions = append(theme.Questions, q)
		}
	}
	d, e := updateInAccess(ctx, a, packID, base, Save{Document: prior.Document}, true)
	if e != nil {
		return Card{}, e
	}
	if e = mcpRecord(ctx, a, key, "upsert_questions", hash, d.Card); e != nil {
		return Card{}, e
	}
	if e = a.tx.Commit(ctx); e != nil {
		return Card{}, e
	}
	return d.Card, nil
}
