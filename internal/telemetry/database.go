package telemetry

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"sync"
	"time"
)

var uploadStates = []string{"uploading", "queued", "processing", "ready", "failed"}
var aiStates = []string{"queued", "running", "ready", "failed", "cancelled", "uncertain"}

type databaseSnapshot struct {
	uploads, jobs                          []int64
	spent, reserved, packBytes, themeBytes float64
	acquired, idle, maximum                int32
	acquireSeconds                         float64
	acquiredTotal                          int64
	success                                float64
}
type databaseMetrics struct {
	mu       sync.RWMutex
	snapshot databaseSnapshot
	errors   prometheus.Counter
	duration prometheus.Histogram
	once     sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

func (m *Metrics) initDatabase() {
	m.database = &databaseMetrics{done: make(chan struct{})}
	d := m.database
	d.errors = prometheus.NewCounter(prometheus.CounterOpts{Name: "si_database_sample_errors_total", Help: "Failed or cancelled bounded operational aggregate reads."})
	d.duration = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "si_database_sample_seconds", Help: "Duration of the bounded operational database aggregate read.", Buckets: buckets})
	m.Registry.MustRegister(d.errors, d.duration)
	gauge := func(name, help string, fn func(databaseSnapshot) float64) {
		m.Registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, func() float64 { d.mu.RLock(); defer d.mu.RUnlock(); return fn(d.snapshot) }))
	}
	gauge("si_database_sample_last_success_timestamp_seconds", "Unix time of the last complete successful database sample; zero means never.", func(s databaseSnapshot) float64 { return s.success })
	gauge("si_ai_spent_usd", "Known retained AI ledger expense in the current UTC calendar month, not the provider invoice.", func(s databaseSnapshot) float64 { return s.spent })
	gauge("si_ai_reserved_usd", "Retained AI reservations across all months, including uncertain outcomes.", func(s databaseSnapshot) float64 { return s.reserved })
	gauge("si_library_logical_bytes", "Logical bytes across all retained pack revisions; not physical deduplicated object storage.", func(s databaseSnapshot) float64 { return s.packBytes })
	gauge("si_themes_logical_bytes", "Logical bytes across all retained theme versions; not physical deduplicated object storage.", func(s databaseSnapshot) float64 { return s.themeBytes })
	gauge("si_db_pool_acquired", "Acquired DB pool connections at last successful sample.", func(s databaseSnapshot) float64 { return float64(s.acquired) })
	gauge("si_db_pool_idle", "Idle DB pool connections at last successful sample.", func(s databaseSnapshot) float64 { return float64(s.idle) })
	gauge("si_db_pool_maximum", "Configured DB pool maximum connections.", func(s databaseSnapshot) float64 { return float64(s.maximum) })
	m.Registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "si_db_pool_acquire_seconds_total", Help: "Cumulative successful pool acquisition duration at last sample."}, func() float64 { d.mu.RLock(); defer d.mu.RUnlock(); return d.snapshot.acquireSeconds }))
	m.Registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "si_db_pool_acquisitions_total", Help: "Cumulative successful pool acquisitions at last sample."}, func() float64 { d.mu.RLock(); defer d.mu.RUnlock(); return float64(d.snapshot.acquiredTotal) }))
	for i, status := range uploadStates {
		m.Registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "si_upload_jobs", Help: "Retained upload jobs by fixed status at last complete sample.", ConstLabels: prometheus.Labels{"status": status}}, func() float64 {
			d.mu.RLock()
			defer d.mu.RUnlock()
			if i >= len(d.snapshot.uploads) {
				return 0
			}
			return float64(d.snapshot.uploads[i])
		}))
	}
	for i, status := range aiStates {
		m.Registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "si_ai_jobs", Help: "Retained AI jobs by fixed status at last complete sample.", ConstLabels: prometheus.Labels{"status": status}}, func() float64 {
			d.mu.RLock()
			defer d.mu.RUnlock()
			if i >= len(d.snapshot.jobs) {
				return 0
			}
			return float64(d.snapshot.jobs[i])
		}))
	}
}

// RefreshDatabase is a single bounded SQL snapshot. Scrapes never call it.
func (m *Metrics) RefreshDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	start := time.Now()
	defer func() { m.database.duration.Observe(time.Since(start).Seconds()) }()
	var sample databaseSnapshot
	e := pool.QueryRow(ctx, `SELECT
 (SELECT ARRAY[count(*) FILTER(WHERE status='uploading'),count(*) FILTER(WHERE status='queued'),count(*) FILTER(WHERE status='processing'),count(*) FILTER(WHERE status='ready'),count(*) FILTER(WHERE status='failed')] FROM uploads),
 (SELECT ARRAY[count(*) FILTER(WHERE status='queued'),count(*) FILTER(WHERE status='running'),count(*) FILTER(WHERE status='ready'),count(*) FILTER(WHERE status='failed'),count(*) FILTER(WHERE status='cancelled'),count(*) FILTER(WHERE status='uncertain')] FROM ai_jobs),
 (SELECT COALESCE(sum(spent) FILTER(WHERE month=date_trunc('month',clock_timestamp() AT TIME ZONE 'UTC')::date),0)::double precision/1000000 FROM ai_monthly_usage),
 (SELECT COALESCE(sum(reserved),0)::double precision/1000000 FROM ai_monthly_usage),
 (SELECT COALESCE(sum(size_bytes),0)::double precision FROM library_revisions),
 (SELECT COALESCE(sum(size_bytes),0)::double precision FROM theme_versions)`).Scan(&sample.uploads, &sample.jobs, &sample.spent, &sample.reserved, &sample.packBytes, &sample.themeBytes)
	if e != nil {
		m.database.errors.Inc()
		return e
	}
	stat := pool.Stat()
	sample.acquired = stat.AcquiredConns()
	sample.idle = stat.IdleConns()
	sample.maximum = stat.MaxConns()
	sample.acquireSeconds = stat.AcquireDuration().Seconds()
	sample.acquiredTotal = stat.AcquireCount()
	sample.success = float64(time.Now().Unix())
	m.database.mu.Lock()
	m.database.snapshot = sample
	m.database.mu.Unlock()
	return nil
}

// WatchDatabase starts at most one sequential sampler. Stop is safe to repeat.
func (m *Metrics) WatchDatabase(ctx context.Context, pool *pgxpool.Pool) func() {
	d := m.database
	d.once.Do(func() {
		child, cancel := context.WithCancel(ctx)
		d.cancel = cancel
		go func() {
			defer close(d.done)
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				if child.Err() != nil {
					return
				}
				_ = m.RefreshDatabase(child, pool)
				select {
				case <-child.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
	return func() { d.cancel(); <-d.done }
}
