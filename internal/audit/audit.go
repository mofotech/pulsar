// Package audit records who changed what through the API.
//
// Every mutating request (POST/PUT/PATCH/DELETE under /v1) is recorded by one
// global middleware, so new endpoints are covered without per-handler code.
// Authentication events that bypass the auth middleware (login, logout, SSO)
// are recorded explicitly by their handlers. Request and response bodies are
// never stored — they carry passwords, client secrets and tokens.
package audit

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/store/postgres"
	"github.com/agomez/pulsar/pkg/retry"
)

// writeTimeout bounds every audit write — background batches and inline
// fallbacks alike — so a stalled database can't hang requests or shutdown.
const writeTimeout = 10 * time.Second

// drainTimeout bounds how long Close waits for in-flight audited requests
// (ones still running after the HTTP server's own shutdown deadline).
const drainTimeout = 15 * time.Second

// Outcomes.
const (
	Success = "success"
	Failure = "failure"
	Denied  = "denied"
)

// Event is one audit record.
type Event struct {
	ID         int64     `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	OrgID      string    `json:"org_id,omitempty"` // the actor's org
	// TargetOrgID is the org that owns the affected resource. It differs
	// from OrgID when a platform admin acts in another org; org admins see
	// events where either is theirs.
	TargetOrgID  string `json:"target_org_id,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	ActorEmail   string `json:"actor_email,omitempty"`
	Action       string `json:"action"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id,omitempty"`
	Outcome      string `json:"outcome"`
	Method       string `json:"method,omitempty"`
	Route        string `json:"route,omitempty"`
	Path         string `json:"path,omitempty"`
	Status       int    `json:"status,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	SourceIP     string `json:"source_ip,omitempty"`
	DurationMS   int    `json:"duration_ms,omitempty"`
}

// Recorder writes events to Postgres from a background worker so request
// latency doesn't depend on the audit insert. If the queue is full, Record
// writes inline instead of dropping the event: audit completeness wins over
// latency under overload.
type Recorder struct {
	db    *postgres.DB
	log   *zap.Logger
	queue chan Event
	wg    sync.WaitGroup

	mu     sync.RWMutex // guards closed vs. sends on queue
	closed bool

	dropped  atomic.Int64 // events that could not be written at all
	inflight atomic.Int64 // requests inside Middleware that have yet to Record
}

// Dropped reports how many events have been lost because they could not be
// written even after retries. Export it once the controller serves /metrics.
func (r *Recorder) Dropped() int64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

// NewRecorder starts the background writer; call Close on shutdown to flush.
func NewRecorder(db *postgres.DB, log *zap.Logger) *Recorder {
	r := &Recorder{db: db, log: log, queue: make(chan Event, 4096)}
	r.wg.Add(1)
	go r.run()
	return r
}

// Record queues an event. Safe on a nil Recorder (auditing disabled).
func (r *Recorder) Record(e Event) {
	if r == nil {
		return
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	r.mu.RLock()
	queued := false
	if !r.closed {
		select {
		case r.queue <- e:
			queued = true
		default:
			r.log.Warn("audit queue full; writing inline")
		}
	}
	// A request finishing during shutdown, or a full queue: persist it
	// inline. The lock is released first so a slow write can't block Close.
	r.mu.RUnlock()
	if !queued {
		r.write([]Event{e})
	}
}

// Close waits (up to drainTimeout) for in-flight audited requests to record
// their events, then flushes the queue and stops the writer. Call it before
// closing the DB pool: a request still running when the HTTP server's
// shutdown deadline passes would otherwise record against a closed pool.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	deadline := time.Now().Add(drainTimeout)
	for r.inflight.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := r.inflight.Load(); n > 0 {
		r.log.Warn("audit: shutting down with requests still in flight; their events may be lost",
			zap.Int64("requests", n))
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()
	r.wg.Wait()
}

func (r *Recorder) run() {
	defer r.wg.Done()
	batch := make([]Event, 0, 64)
	for e := range r.queue {
		batch = append(batch[:0], e)
		// Drain whatever else is already queued into the same batch.
	drain:
		for len(batch) < cap(batch) {
			select {
			case e, ok := <-r.queue:
				if !ok {
					break drain
				}
				batch = append(batch, e)
			default:
				break drain
			}
		}
		r.write(batch)
	}
}

// write persists events, bounded by writeTimeout for the batch attempt and
// again per event in the fallback, so a batch that timed out in Begin still
// gets its retries.
func (r *Recorder) write(events []Event) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	err := r.writeBatch(ctx, events)
	cancel()
	if err != nil {
		// Any batch failure — Begin, a bad row, or Commit (e.g. a failover or
		// connection reset) — falls back to row-by-row writes with retries,
		// so only rows that are themselves unwritable are lost. A Commit
		// error is ambiguous (the batch may have landed), so this can
		// duplicate events; for an audit log a duplicate beats a gap.
		r.log.Warn("audit: batch write failed; retrying row by row",
			zap.Error(err), zap.Int("events", len(events)))
		r.writeEach(events)
	}
}

func (r *Recorder) writeBatch(ctx context.Context, events []Event) error {
	tx, err := r.db.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, e := range events {
		if _, err := tx.Exec(ctx, insertSQL, insertArgs(e)...); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// writeEach writes events one at a time, retrying each with backoff, so a
// transient outage or one bad row doesn't take the rest of the batch with it.
// Each event gets its own deadline: one write hanging until timeout (say,
// during a failover) must not use up the time the rest of the batch needs.
func (r *Recorder) writeEach(events []Event) {
	for _, e := range events {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err := retry.Do(ctx, 3, 100*time.Millisecond, func() error {
			_, err := r.db.Pool().Exec(ctx, insertSQL, insertArgs(e)...)
			return err
		})
		cancel()
		if err != nil {
			n := r.dropped.Add(1)
			r.log.Error("audit: dropping unwritable event", zap.Error(err),
				zap.String("action", e.Action), zap.String("resource_type", e.ResourceType),
				zap.String("request_id", e.RequestID), zap.Int64("dropped_total", n))
		}
	}
}

// StartRetention deletes events older than retention once a day (and once at
// start). retention <= 0 keeps events forever.
func (r *Recorder) StartRetention(ctx context.Context, retention time.Duration) {
	if r == nil || retention <= 0 {
		return
	}
	prune := func() {
		// Delete in bounded batches, each with its own timeout: one DELETE
		// of millions of expired rows could be cancelled (and rolled back)
		// every day and never make progress.
		cutoff := time.Now().Add(-retention)
		var total int64
		for ctx.Err() == nil {
			c, cancel := context.WithTimeout(ctx, time.Minute)
			tag, err := r.db.Pool().Exec(c,
				`DELETE FROM audit_events WHERE id IN (
				   SELECT id FROM audit_events WHERE occurred_at < $1 LIMIT $2)`,
				cutoff, retentionBatch)
			cancel()
			if err != nil {
				r.log.Error("audit retention", zap.Error(err), zap.Int64("deleted_so_far", total))
				return
			}
			total += tag.RowsAffected()
			if tag.RowsAffected() < retentionBatch {
				break
			}
		}
		if total > 0 {
			r.log.Info("audit retention pruned events", zap.Int64("deleted", total))
		}
	}
	go func() {
		prune()
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				prune()
			}
		}
	}()
}

// retentionBatch is how many expired events one retention DELETE removes.
const retentionBatch = 10000

// target_org_id falls back to the org owning the (validated) project, then
// to the actor's own org.
const insertSQL = `
	INSERT INTO audit_events
	  (occurred_at, org_id, project_id, user_id, actor_email, action, resource_type,
	   resource_id, outcome, method, route, path, status, request_id, source_ip, duration_ms,
	   target_org_id)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
	        COALESCE($17::uuid, (SELECT org_id FROM projects WHERE id = $3::uuid), $2::uuid))`

func insertArgs(e Event) []any {
	return []any{
		e.OccurredAt, uuidOrNil(e.OrgID), uuidOrNil(e.ProjectID), uuidOrNil(e.UserID),
		nilIfEmpty(e.ActorEmail), e.Action, e.ResourceType, nilIfEmpty(e.ResourceID), e.Outcome,
		nilIfEmpty(e.Method), nilIfEmpty(e.Route), nilIfEmpty(e.Path), e.Status,
		nilIfEmpty(e.RequestID), nilIfEmpty(e.SourceIP), e.DurationMS, uuidOrNil(e.TargetOrgID),
	}
}

// uuidOrNil keeps non-UUID strings (e.g. a garbage claim) from failing the
// insert of an otherwise valid event.
func uuidOrNil(s string) any {
	if len(s) != 36 {
		return nil // also rejects the braced/urn forms uuid.Parse accepts
	}
	if _, err := uuid.Parse(s); err != nil {
		return nil
	}
	return s
}

// maxEmailLen is the longest valid address (RFC 5321 path limit).
const maxEmailLen = 254

// SanitizeEmail returns s if it is plausibly an email address and "" if not.
// Login attempts record what was typed, and people regularly type their
// password into the username box; that must not end up in a log admins can
// read, and neither should multi-megabyte junk.
func SanitizeEmail(s string) string {
	s = strings.TrimSpace(s)
	at := strings.IndexByte(s, '@')
	if len(s) > maxEmailLen || at < 1 || at != strings.LastIndexByte(s, '@') ||
		at == len(s)-1 ||
		strings.ContainsFunc(s, func(c rune) bool { return c <= ' ' || c == 0x7f }) {
		return ""
	}
	return s
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
