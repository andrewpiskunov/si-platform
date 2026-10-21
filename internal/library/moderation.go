package library

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
)

var reportIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func validModerationReason(reason string, required bool) bool {
	if !utf8.ValidString(reason) || len(reason) > 8000 || utf8.RuneCountInString(reason) > 2000 || required && strings.TrimSpace(reason) == "" {
		return false
	}
	for _, r := range reason {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

func (s *Service) DecisionHistory(ctx context.Context, actor accounts.Identity, packID string, afterID int64, limit int) (DecisionPage, error) {
	a, err := s.begin(ctx, actor, "drafts:read", false, packID)
	if err != nil {
		return DecisionPage{}, err
	}
	defer a.close()
	if err = a.reader(); err != nil {
		return DecisionPage{}, err
	}
	if afterID < 0 || limit < 1 || limit > 50 {
		return DecisionPage{}, ErrInvalid
	}
	rows, err := a.tx.Query(ctx, `SELECT id,revision,decision,reason,created_at FROM library_decisions WHERE pack_id=$1 AND id>$2 ORDER BY id LIMIT $3`, packID, afterID, limit+1)
	if err != nil {
		return DecisionPage{}, err
	}
	defer rows.Close()
	page := DecisionPage{Items: []DecisionRecord{}}
	for rows.Next() {
		var r DecisionRecord
		if err = rows.Scan(&r.ID, &r.Revision, &r.Decision, &r.Reason, &r.CreatedAt); err != nil {
			return DecisionPage{}, err
		}
		page.Items = append(page.Items, r)
	}
	if err = rows.Err(); err != nil {
		return DecisionPage{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextAfterID = page.Items[limit-1].ID
	}
	return page, nil
}

const reportColumns = "id,pack_id,revision,reporter_id,reason,status,created_at,resolution,resolution_reason,resolved_by,resolved_at"

func scanReport(row scanner) (Report, error) {
	var r Report
	err := row.Scan(&r.ID, &r.PackID, &r.Revision, &r.ReporterID, &r.Reason, &r.Status, &r.CreatedAt, &r.Resolution, &r.ResolutionReason, &r.ResolvedBy, &r.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}
func (s *Service) ReportPage(ctx context.Context, actor accounts.Identity, status, after string, limit int) (ReportsPage, error) {
	a, err := s.begin(ctx, actor, "", true, "")
	if err != nil {
		return ReportsPage{}, err
	}
	defer a.close()
	if !moderator(a.actor) {
		return ReportsPage{}, ErrForbidden
	}
	if status != "open" && status != "resolved" || after != "" && !reportIDPattern.MatchString(after) || limit < 1 || limit > 50 {
		return ReportsPage{}, ErrInvalid
	}
	rows, err := a.tx.Query(ctx, "SELECT "+reportColumns+" FROM library_reports WHERE status=$1 AND id>$2 ORDER BY id LIMIT $3", status, after, limit+1)
	if err != nil {
		return ReportsPage{}, err
	}
	defer rows.Close()
	page := ReportsPage{Items: []Report{}}
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return ReportsPage{}, err
		}
		page.Items = append(page.Items, r)
	}
	if err = rows.Err(); err != nil {
		return ReportsPage{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextAfter = page.Items[limit-1].ID
	}
	return page, nil
}

// Resolution deliberately locks only the actor and report, allowing closure even
// after the pack or its owner has been deleted. No later pack lock is taken.
func (s *Service) ResolveReport(ctx context.Context, actor accounts.Identity, reportID, disposition, reason string) (Report, error) {
	a, err := s.begin(ctx, actor, "", true, "")
	if err != nil {
		return Report{}, err
	}
	defer a.close()
	if !moderator(a.actor) {
		return Report{}, ErrForbidden
	}
	if !reportIDPattern.MatchString(reportID) || disposition != "dismissed" && disposition != "resolved" || !validModerationReason(reason, true) {
		return Report{}, ErrInvalid
	}
	r, err := scanReport(a.tx.QueryRow(ctx, "SELECT "+reportColumns+" FROM library_reports WHERE id=$1 FOR UPDATE", reportID))
	if err != nil {
		return Report{}, err
	}
	if r.Status == "resolved" {
		if r.Resolution != disposition || r.ResolutionReason != reason {
			return Report{}, ErrConflict
		}
		return r, nil
	}
	r, err = scanReport(a.tx.QueryRow(ctx, "UPDATE library_reports SET status='resolved',resolution=$2,resolution_reason=$3,resolved_by=$4,resolved_at=clock_timestamp() WHERE id=$1 RETURNING "+reportColumns, reportID, disposition, reason, a.actor.User.ID))
	if err != nil {
		return Report{}, err
	}
	if err = a.tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	return r, nil
}
