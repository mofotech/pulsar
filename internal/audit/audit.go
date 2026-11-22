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
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/store/postgres"
)

// Outcomes.
const (
	Success = "success"
	Failure = "failure"
	Denied  = "denied"
)

// Event is one audit record.
type Event struct {
	ID           int64     `json:"id"`
	OccurredAt   time.Time `json:"occurred_at"`
	OrgID        string    `json:"org_id,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	UserID       string    `json:"user_id,omitempty"`
	ActorEmail   string    `json:"actor_email,omitempty"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id,omitempty"`
	Outcome      string    `json:"outcome"`
	Method       string    `json:"method,omitempty"`
	Route        string    `json:"route,omitempty"`
	Path         string    `json:"path,omitempty"`
	Status       int       `json:"status,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	SourceIP     string    `json:"source_ip,omitempty"`
	DurationMS   int       `json:"duration_ms,omitempty"`
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
	defer r.mu.RUnlock()
	if r.closed {
		// A request finishing during shutdown: still persist it.
		r.write(context.Background(), []Event{e})
		return
	}
	select {
	case r.queue <- e:
	default:
		r.log.Warn("audit queue full; writing inline")
		r.write(context.Background(), []Event{e})
	}
}

// Close flushes queued events and stops the writer.
func (r *Recorder) Close() {
	if r == nil {
		return
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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r.write(ctx, batch)
		cancel()
	}
}

func (r *Recorder) write(ctx context.Context, events []Event) {
	tx, err := r.db.Pool().Begin(ctx)
	if err != nil {
		r.log.Error("audit: begin", zap.Error(err), zap.Int("events", len(events)))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, e := range events {
		if _, err := tx.Exec(ctx, insertSQL, insertArgs(e)...); err != nil {
			// Never lose the whole batch to one bad row: log it, keep going.
			r.log.Error("audit: insert", zap.Error(err), zap.String("action", e.Action),
				zap.String("resource_type", e.ResourceType), zap.String("request_id", e.RequestID))
			_ = tx.Rollback(ctx)
			r.writeEach(ctx, events)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		r.log.Error("audit: commit", zap.Error(err), zap.Int("events", len(events)))
	}
}

// writeEach retries a failed batch row by row, so only the bad row is lost.
func (r *Recorder) writeEach(ctx context.Context, events []Event) {
	for _, e := range events {
		_, err := r.db.Pool().Exec(ctx, insertSQL, insertArgs(e)...)
		if err != nil {
			r.log.Error("audit: dropping unwritable event", zap.Error(err),
				zap.String("action", e.Action), zap.String("request_id", e.RequestID))
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
		c, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		tag, err := r.db.Pool().Exec(c,
			`DELETE FROM audit_events WHERE occurred_at < $1`, time.Now().Add(-retention))
		if err != nil {
			r.log.Error("audit retention", zap.Error(err))
			return
		}
		if n := tag.RowsAffected(); n > 0 {
			r.log.Info("audit retention pruned events", zap.Int64("deleted", n))
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

const insertSQL = `
	INSERT INTO audit_events
	  (occurred_at, org_id, project_id, user_id, actor_email, action, resource_type,
	   resource_id, outcome, method, route, path, status, request_id, source_ip, duration_ms)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`

func insertArgs(e Event) []any {
	return []any{
		e.OccurredAt, uuidOrNil(e.OrgID), uuidOrNil(e.ProjectID), uuidOrNil(e.UserID),
		nilIfEmpty(e.ActorEmail), e.Action, e.ResourceType, nilIfEmpty(e.ResourceID), e.Outcome,
		nilIfEmpty(e.Method), nilIfEmpty(e.Route), nilIfEmpty(e.Path), e.Status,
		nilIfEmpty(e.RequestID), nilIfEmpty(e.SourceIP), e.DurationMS,
	}
}

// uuidOrNil keeps non-UUID strings (e.g. a garbage claim) from failing the
// insert of an otherwise valid event.
func uuidOrNil(s string) any {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return nil
	}
	return s
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
