package ai

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/aiformat"
	"siplatform/internal/aiprovider"
	"sync"
	"time"
)

var (
	ErrDisabled    = errors.New("ai_disabled")
	ErrInvalid     = errors.New("invalid_input")
	ErrConflict    = errors.New("conflict")
	ErrLimit       = errors.New("limit_exceeded")
	ErrNotFound    = errors.New("not_found")
	ErrUnavailable = errors.New("unavailable")
)

type Options struct {
	Provider                            aiprovider.Provider
	Model                               string
	InputRate, OutputRate, MonthlyLimit int64
	MaxOutputTokens                     int
	ProviderLabel                       string
}
type Status struct {
	Enabled      bool   `json:"enabled"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	MonthlyLimit int64  `json:"monthly_limit"`
	Spent        int64  `json:"spent"`
	Reserved     int64  `json:"reserved"`
	Remaining    int64  `json:"remaining"`
}
type Quote struct {
	ID              string           `json:"id"`
	Request         aiformat.Request `json:"request"`
	Model           string           `json:"model"`
	InputTokens     int64            `json:"input_tokens"`
	MaxOutputTokens int              `json:"max_output_tokens"`
	MaximumCost     int64            `json:"maximum_cost"`
	ExpiresAt       time.Time        `json:"expires_at"`
}
type Job struct {
	ID              string           `json:"id"`
	Status          string           `json:"status"`
	Model           string           `json:"model"`
	Request         aiformat.Request `json:"request"`
	Result          *aiformat.Result `json:"result,omitempty"`
	MaximumCost     int64            `json:"maximum_cost"`
	ActualCost      *int64           `json:"actual_cost,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	CompletedAt     *time.Time       `json:"completed_at,omitempty"`
	CancelRequested bool             `json:"cancel_requested"`
	Error           string           `json:"error,omitempty"`
}
type Page struct {
	Jobs []Job  `json:"jobs"`
	Next string `json:"next,omitempty"`
}

var countSlots = make(chan struct{}, 2)

type Service struct {
	pool    *pgxpool.Pool
	options Options
	mu      sync.Mutex
	running map[string]context.CancelFunc
	started bool
}

func New(p *pgxpool.Pool, o Options) (*Service, error) {
	if p == nil {
		return nil, ErrInvalid
	}
	if o.Provider != nil && (o.Model == "" || len(o.Model) > 200 || o.InputRate <= 0 || o.OutputRate <= 0 || o.MonthlyLimit <= 0 || o.InputRate > 1e12 || o.OutputRate > 1e12 || o.MonthlyLimit > 1e12 || o.MaxOutputTokens < 256 || o.MaxOutputTokens > 64000) {
		return nil, ErrInvalid
	}
	return &Service{pool: p, options: o, running: map[string]context.CancelFunc{}}, nil
}
