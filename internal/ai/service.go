package ai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"math"
	"siplatform/internal/accounts"
	"siplatform/internal/aiformat"
	"strings"
)

func cost(tokens, rate int64) (int64, error) {
	if tokens < 0 || rate < 0 || rate != 0 && tokens > (math.MaxInt64-999999)/rate {
		return 0, ErrInvalid
	}
	return (tokens*rate + 999999) / 1000000, nil
}
func newID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func auth(ctx context.Context, tx pgx.Tx, a accounts.Identity) error {
	var id, kind string
	var scopes []string
	e := tx.QueryRow(ctx, `SELECT id FROM account_users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, a.User.ID).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return accounts.ErrUnauthorized
	}
	if e != nil {
		return e
	}
	e = tx.QueryRow(ctx, `SELECT kind,scopes FROM account_credentials WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, a.SessionID, a.User.ID).Scan(&kind, &scopes)
	if errors.Is(e, pgx.ErrNoRows) {
		return accounts.ErrUnauthorized
	}
	if e != nil {
		return e
	}
	if kind == "api" {
		for _, v := range scopes {
			if v == "ai:generate" {
				return nil
			}
		}
		return accounts.ErrForbidden
	}
	return nil
}
func (s *Service) begin(ctx context.Context, a accounts.Identity, guard bool) (pgx.Tx, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	if guard {
		_, e = tx.Exec(ctx, `SELECT id FROM ai_admission WHERE id=1 FOR UPDATE`)
	}
	if e == nil {
		e = auth(ctx, tx, a)
	}
	if e != nil {
		tx.Rollback(ctx)
		return nil, e
	}
	return tx, nil
}
func (s *Service) Status(ctx context.Context, a accounts.Identity) (Status, error) {
	v := Status{Enabled: s.options.Provider != nil, Provider: s.options.ProviderLabel, Model: s.options.Model, MonthlyLimit: s.options.MonthlyLimit}
	tx, e := s.begin(ctx, a, false)
	if e != nil {
		return v, e
	}
	defer tx.Rollback(ctx)
	e = tx.QueryRow(ctx, `SELECT spent,reserved FROM ai_monthly_usage WHERE owner_id=$1 AND month=date_trunc('month',clock_timestamp() AT TIME ZONE 'UTC')::date`, a.User.ID).Scan(&v.Spent, &v.Reserved)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return v, e
	}
	v.Remaining = max(int64(0), v.MonthlyLimit-v.Spent-v.Reserved)
	return v, tx.Commit(ctx)
}
func (s *Service) Quote(ctx context.Context, a accounts.Identity, r aiformat.Request) (Quote, error) {
	var q Quote
	r.Topic = strings.TrimSpace(r.Topic)
	r.Audience = strings.TrimSpace(r.Audience)
	if aiformat.ValidateRequest(r) != nil {
		return q, ErrInvalid
	}
	if s.options.Provider == nil {
		return q, ErrDisabled
	}
	select {
	case countSlots <- struct{}{}:
		defer func() { <-countSlots }()
	default:
		return q, ErrLimit
	}
	tx, e := s.begin(ctx, a, false)
	if e != nil {
		return q, e
	}
	defer tx.Rollback(ctx)
	var recent bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ai_quotes WHERE owner_id=$1 AND created_at>clock_timestamp()-interval '10 seconds')`, a.User.ID).Scan(&recent)
	if e != nil {
		return q, e
	}
	if recent {
		return q, ErrLimit
	}
	q = Quote{ID: newID(), Request: r, Model: s.options.Model, MaxOutputTokens: s.options.MaxOutputTokens}
	b, _ := json.Marshal(r)
	e = tx.QueryRow(ctx, `INSERT INTO ai_quotes(owner_id,id,credential_id,request,model,input_rate,output_rate,max_output_tokens) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(owner_id) DO UPDATE SET id=EXCLUDED.id,credential_id=EXCLUDED.credential_id,request=EXCLUDED.request,model=EXCLUDED.model,input_rate=EXCLUDED.input_rate,output_rate=EXCLUDED.output_rate,max_output_tokens=EXCLUDED.max_output_tokens,input_tokens=NULL,maximum_cost=NULL,created_at=clock_timestamp(),expires_at=clock_timestamp()+interval '5 minutes' RETURNING expires_at`, a.User.ID, q.ID, a.SessionID, b, q.Model, s.options.InputRate, s.options.OutputRate, q.MaxOutputTokens).Scan(&q.ExpiresAt)
	if e != nil {
		return q, e
	}
	if e = tx.Commit(ctx); e != nil {
		return q, e
	}
	countCtx, cancel := context.WithTimeout(ctx, 15e9)
	defer cancel()
	q.InputTokens, e = s.options.Provider.Count(countCtx, r, q.Model, q.MaxOutputTokens)
	if e != nil {
		return Quote{}, ErrUnavailable
	}
	if q.InputTokens < 0 || q.InputTokens > 32000 {
		return Quote{}, ErrInvalid
	}
	in, _ := cost(q.InputTokens, s.options.InputRate)
	out, _ := cost(int64(q.MaxOutputTokens), s.options.OutputRate)
	q.MaximumCost = in + out
	tx, e = s.begin(ctx, a, false)
	if e != nil {
		return Quote{}, e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE ai_quotes SET input_tokens=$3,maximum_cost=$4 WHERE owner_id=$1 AND id=$2 AND expires_at>clock_timestamp()`, a.User.ID, q.ID, q.InputTokens, q.MaximumCost)
	if e != nil {
		return Quote{}, e
	}
	if tag.RowsAffected() != 1 {
		return Quote{}, ErrConflict
	}
	return q, tx.Commit(ctx)
}

const columns = `id,status,model,request,result,maximum_cost,actual_cost,created_at,completed_at,cancel_requested,error`

type scanner interface{ Scan(...any) error }

func scanJob(row scanner) (Job, error) {
	var j Job
	var req, res []byte
	e := row.Scan(&j.ID, &j.Status, &j.Model, &req, &res, &j.MaximumCost, &j.ActualCost, &j.CreatedAt, &j.CompletedAt, &j.CancelRequested, &j.Error)
	if errors.Is(e, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	if e != nil {
		return j, e
	}
	if e = json.Unmarshal(req, &j.Request); e != nil {
		return j, e
	}
	if res != nil {
		e = json.Unmarshal(res, &j.Result)
	}
	return j, e
}
func (s *Service) Start(ctx context.Context, a accounts.Identity, qid, key string) (Job, error) {
	var j Job
	if key == "" || len(key) > 128 || len(qid) > 128 {
		return j, ErrInvalid
	}
	tx, e := s.begin(ctx, a, true)
	if e != nil {
		return j, e
	}
	defer tx.Rollback(ctx)
	hash := sha256.Sum256([]byte(qid))
	h := hex.EncodeToString(hash[:])
	var oldHash string
	e = tx.QueryRow(ctx, `SELECT request_hash FROM ai_jobs WHERE owner_id=$1 AND idempotency_key=$2`, a.User.ID, key).Scan(&oldHash)
	if e == nil {
		if oldHash != h {
			return j, ErrConflict
		}
		j, e = scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM ai_jobs WHERE owner_id=$1 AND idempotency_key=$2`, a.User.ID, key))
		if e != nil {
			return j, e
		}
		return j, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return j, e
	}
	if s.options.Provider == nil {
		return j, ErrDisabled
	}
	var req []byte
	var model string
	var ir, or, it, maximum int64
	var mt int
	e = tx.QueryRow(ctx, `SELECT request,model,input_rate,output_rate,input_tokens,max_output_tokens,maximum_cost FROM ai_quotes WHERE owner_id=$1 AND id=$2 AND expires_at>clock_timestamp() AND maximum_cost IS NOT NULL FOR UPDATE`, a.User.ID, qid).Scan(&req, &model, &ir, &or, &it, &mt, &maximum)
	if errors.Is(e, pgx.ErrNoRows) {
		return j, ErrConflict
	}
	if e != nil {
		return j, e
	}
	var total, own int
	e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE owner_id=$1) FROM ai_jobs WHERE status IN ('queued','running')`, a.User.ID).Scan(&total, &own)
	if e != nil {
		return j, e
	}
	if total >= 20 || own != 0 {
		return j, ErrLimit
	}
	_, e = tx.Exec(ctx, `INSERT INTO ai_monthly_usage(owner_id,month) VALUES($1,date_trunc('month',CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date) ON CONFLICT DO NOTHING`, a.User.ID)
	if e != nil {
		return j, e
	}
	tag, e := tx.Exec(ctx, `UPDATE ai_monthly_usage SET reserved=reserved+$2 WHERE owner_id=$1 AND month=date_trunc('month',CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date AND spent+reserved+$2<=$3`, a.User.ID, maximum, s.options.MonthlyLimit)
	if e != nil {
		return j, e
	}
	if tag.RowsAffected() != 1 {
		return j, ErrLimit
	}
	j, e = scanJob(tx.QueryRow(ctx, `INSERT INTO ai_jobs(id,owner_id,credential_id,status,request,model,quote_id,idempotency_key,request_hash,month,input_rate,output_rate,input_tokens,max_output_tokens,maximum_cost) VALUES($1,$2,$3,'queued',$4,$5,$6,$7,$8,date_trunc('month',CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date,$9,$10,$11,$12,$13) RETURNING `+columns, newID(), a.User.ID, a.SessionID, req, model, qid, key, h, ir, or, it, mt, maximum))
	if e != nil {
		return j, e
	}
	return j, tx.Commit(ctx)
}
func (s *Service) Get(ctx context.Context, a accounts.Identity, id string) (Job, error) {
	tx, e := s.begin(ctx, a, false)
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback(ctx)
	j, e := scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM ai_jobs WHERE owner_id=$1 AND id=$2`, a.User.ID, id))
	if e != nil {
		return j, e
	}
	return j, tx.Commit(ctx)
}
func (s *Service) List(ctx context.Context, a accounts.Identity, after string) (Page, error) {
	p := Page{Jobs: []Job{}}
	tx, e := s.begin(ctx, a, false)
	if e != nil {
		return p, e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT `+columns+` FROM ai_jobs WHERE owner_id=$1 AND ($2='' OR (created_at,id)<(SELECT created_at,id FROM ai_jobs WHERE owner_id=$1 AND id=$2)) ORDER BY created_at DESC,id DESC LIMIT 51`, a.User.ID, after)
	if e != nil {
		return p, e
	}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			rows.Close()
			return p, e
		}
		p.Jobs = append(p.Jobs, j)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return p, e
	}
	if len(p.Jobs) > 50 {
		p.Jobs = p.Jobs[:50]
		p.Next = p.Jobs[49].ID
	}
	return p, tx.Commit(ctx)
}
func (s *Service) Cancel(ctx context.Context, a accounts.Identity, id string) (Job, error) {
	tx, e := s.begin(ctx, a, false)
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback(ctx)
	j, e := scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM ai_jobs WHERE owner_id=$1 AND id=$2 FOR UPDATE`, a.User.ID, id))
	if e != nil {
		return j, e
	}
	if j.Status == "queued" {
		if e = refund(ctx, tx, id); e != nil {
			return j, e
		}
		_, e = tx.Exec(ctx, `UPDATE ai_jobs SET status='cancelled',actual_cost=0,completed_at=clock_timestamp(),cancel_requested=true WHERE id=$1`, id)
	} else if j.Status == "running" {
		_, e = tx.Exec(ctx, `UPDATE ai_jobs SET cancel_requested=true WHERE id=$1`, id)
	}
	if e != nil {
		return j, e
	}
	j, e = scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM ai_jobs WHERE id=$1`, id))
	if e != nil {
		return j, e
	}
	if e = tx.Commit(ctx); e != nil {
		return j, e
	}
	s.mu.Lock()
	if cancel := s.running[id]; cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	return j, nil
}
func refund(ctx context.Context, tx pgx.Tx, id string) error {
	_, e := tx.Exec(ctx, `UPDATE ai_monthly_usage u SET reserved=u.reserved-j.maximum_cost FROM ai_jobs j WHERE j.id=$1 AND u.owner_id=j.owner_id AND u.month=j.month`, id)
	return e
}
