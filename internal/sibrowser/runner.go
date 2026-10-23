package sibrowser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"siplatform/internal/ingest"
	"time"
)

type Options struct {
	Mode, APIURL, Token, Directory string
	InsecureAPI                    bool
	MaxPages, MaxItems             int
	RetryRejected                  bool
	ForceMetadata                  bool
	Source                         *Source
}
type ItemError struct {
	ID    string `json:"id"`
	Stage string `json:"stage"`
	Code  string `json:"code"`
}
type Report struct {
	MetadataUpdated int         `json:"metadata_updated"`
	CoversUpdated   int         `json:"covers_updated"`
	MetadataErrors  []ItemError `json:"metadata_errors"`
	RunID           string      `json:"run_id"`
	Mode            string      `json:"mode"`
	Pages           int         `json:"pages"`
	Discovered      int         `json:"discovered"`
	Published       int         `json:"published"`
	Skipped         int         `json:"skipped"`
	Errors          []ItemError `json:"errors"`
	Complete        bool        `json:"complete"`
	NextPage        int         `json:"next_page"`
}

func Run(ctx context.Context, p *pgxpool.Pool, opt Options) (Report, error) {
	report := Report{Mode: opt.Mode, Errors: []ItemError{}}
	id := make([]byte, 16)
	rand.Read(id)
	report.RunID = hex.EncodeToString(id)
	if opt.Mode != "full" && opt.Mode != "incremental" && opt.Mode != "metadata" || opt.Directory == "" || opt.MaxPages < 0 || opt.MaxItems < 0 {
		return report, ErrConfig
	}
	api, e := newAPI(opt.APIURL, opt.Token, opt.InsecureAPI)
	if e != nil {
		return report, e
	}
	if e = os.MkdirAll(opt.Directory, 0700); e != nil {
		return report, errors.New("scratch_unavailable")
	}
	// Each invocation owns a new scratch directory; no filename is trusted from DB.
	dir, e := os.MkdirTemp(opt.Directory, "run-")
	if e != nil {
		return report, errors.New("scratch_unavailable")
	}
	defer os.RemoveAll(dir)
	l, e := acquire(ctx, p)
	if e != nil {
		return report, e
	}
	defer l.close()
	ctx = l.ctx
	report.NextPage = l.next
	src := opt.Source
	if src == nil {
		src = NewSource(time.Second)
	}
	if opt.Mode == "metadata" {
		e = metadataPass(ctx, l, src, api, opt, &report, false)
		report.Complete = e == nil
		if e == nil && len(report.MetadataErrors) > 0 {
			e = ErrIncomplete
		}
		return report, e
	}
	if e = metadataPass(ctx, l, src, api, opt, &report, true); e != nil {
		return report, e
	}
	if opt.RetryRejected {
		if e = l.retryRejected(); e != nil {
			return report, e
		}
	}
	processed := 0
	process := func() error {
		for {
			if opt.MaxItems > 0 && processed >= opt.MaxItems {
				return ErrIncomplete
			}
			batch, e := l.pending(1)
			if e != nil {
				return e
			}
			if len(batch) == 0 {
				return nil
			}
			r := batch[0]
			r.Attempts++
			processed++
			pack, e := processItem(ctx, l, src, api, dir, r, opt.RetryRejected)
			if e == nil {
				report.Published++
				if e = l.save(r, "published", "", pack, 0); e != nil {
					return e
				}
				if e = metadataOne(ctx, l, src, api, r.Item.ID, &report); e != nil {
					return e
				}
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			code := "sync_failed"
			state := "retryable"
			delay := time.Hour
			var f *Fault
			if errors.As(e, &f) {
				code = f.Code
				delay = max(delay, f.Delay)
				if !f.Retryable {
					state = "rejected"
				}
				if code == "publication_suppressed" || code == "receipt_suppressed" || code == "api_blocked" {
					state = "suppressed"
				}
			}
			if code == "api_http_401" || code == "api_http_403" {
				state = "retryable"
			}
			// r may have acquired an immutable input during processItem: save preserves it.
			if e = l.save(r, state, code, "", delay); e != nil {
				return e
			}
			report.Errors = append(report.Errors, ItemError{r.Item.ID, state, code})
			if code == "api_http_401" || code == "api_http_403" || code == "api_http_429" || code == "processing_draft_quota" {
				return ErrIncomplete
			}
		}
	}
	if e = process(); e != nil {
		return report, e
	}
	full := opt.Mode == "full" || !l.bootstrap || l.reconciling
	page := 1
	if full {
		page = max(1, l.next-2)
	}
	known := 0
	reconcile := l.reconciling
	forwardFrom := 1
	if full {
		forwardFrom = l.next
	}
	budgetPages := 0
	for {
		report.NextPage = page
		if opt.MaxPages > 0 && budgetPages >= opt.MaxPages {
			return report, ErrIncomplete
		}
		listing, e := src.Page(ctx, page)
		if e != nil {
			return report, e
		}
		report.Pages++
		if page >= forwardFrom {
			budgetPages++
		}
		added, oldPage, e := l.discover(listing, full, reconcile)
		if e != nil {
			return report, e
		}
		report.Discovered += added
		report.Skipped += len(listing.Items) - added
		for _, item := range listing.Items {
			if e = metadataOne(ctx, l, src, api, item.ID, &report); e != nil {
				return report, e
			}
		}
		if oldPage {
			known++
		} else {
			known = 0
		}
		if e = process(); e != nil {
			return report, e
		}
		end := listing.Next == 0 || (!full || reconcile) && known >= 3
		if end {
			if full && !reconcile {
				reconcile = true
				forwardFrom = 1
				known = 0
				page = 1
				continue
			}
			if e = l.complete(full); e != nil {
				return report, e
			}
			report.Complete = true
			report.NextPage = 1
			if len(report.Errors) > 0 {
				return report, ErrIncomplete
			}
			return report, nil
		}
		page = listing.Next
	}
}
func processItem(ctx context.Context, l *lease, src *Source, api *apiClient, dir string, r record, retry bool) (string, error) {
	var u ingest.Upload
	var e error
	file := ""
	if r.Input != nil {
		u, e = api.begin(ctx, *r.Input)
		if e != nil {
			return "", e
		}
		if u.Status == "failed" && (retry || u.Error == "expired" || u.Error == "staging_unavailable") {
			u, e = api.retry(ctx, *r.Input)
			if e != nil {
				return "", e
			}
		}
	}
	if r.Input == nil || u.Status == "uploading" {
		var sha string
		var size int64
		file, sha, size, e = src.Download(ctx, r.Item, dir)
		if e != nil {
			return "", e
		}
		defer os.Remove(file)
		if r.Input != nil {
			if r.Input.SHA256 != sha || r.Input.Size != size {
				return "", fault("source_content_changed", false)
			}
		} else {
			r.Input = &ingest.ExternalInput{ExternalID: r.Item.ID, Name: r.Item.ID + ".siq", Size: size, SHA256: sha, Attribution: r.Item.Author}
			if e = r.Input.Validate(); e != nil {
				return "", fault("invalid_source_metadata", false)
			}
			if e = l.save(r, "downloaded", "", "", 0); e != nil {
				return "", e
			}
			u, e = api.begin(ctx, *r.Input)
			if e != nil {
				return "", e
			}
		}
	}
	jobCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if _, e = api.upload(jobCtx, file, *r.Input, u); e != nil {
		return "", e
	}
	if e = l.save(r, "imported", "", u.DraftID, 0); e != nil {
		return "", e
	}
	return api.publish(ctx, r.Item.ID)
}
