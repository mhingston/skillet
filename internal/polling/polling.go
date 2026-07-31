// Package polling provides repository-scoped polling state, leases, jitter,
// and a testable coordinator around repository synchronization.
package polling

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

type Repository struct {
	OrganizationID string
	ID             string
	URL            string
	Ref            string
	PollInterval   time.Duration
}

func (r Repository) Key() string { return r.OrganizationID + "/" + r.ID }

type Status struct {
	LastSeenCommit   string
	LastSuccessfulAt time.Time
	LastFailedAt     time.Time
	LastFailure      string
}

type State interface {
	Get(context.Context, string) (Status, error)
	SaveSuccess(context.Context, string, string, time.Time) error
	SaveFailure(context.Context, string, string, time.Time) error
}

type Lease interface{ Release() error }
type RenewableLease interface {
	Lease
	Renew(context.Context, time.Duration) error
}

type Leases interface {
	Acquire(context.Context, string, time.Duration) (Lease, bool, error)
}

type SyncResult struct {
	Commit                string
	Admitted, Quarantined int
}

type ResolveFunc func(context.Context, Repository) (string, error)
type SyncFunc func(context.Context, Repository, string) (SyncResult, error)

type Outcome string

const (
	Synchronized     Outcome = "synchronized"
	SkippedUnchanged Outcome = "skipped_unchanged"
	SkippedLease     Outcome = "skipped_lease"
)

type RunResult struct {
	Outcome Outcome
	Commit  string
	Sync    SyncResult
}

type Coordinator struct {
	State      State
	Leases     Leases
	Resolve    ResolveFunc
	Sync       SyncFunc
	Clock      func() time.Time
	LeaseTTL   time.Duration
	Audit      func(context.Context, string, Repository, map[string]any) error
	AuditError func(context.Context, Repository, string, error)
	Observe    func(context.Context, Repository, RunResult, time.Duration, error)
}

func (c Coordinator) RunOnce(ctx context.Context, repo Repository) (result RunResult, retErr error) {
	if c.State == nil || c.Leases == nil || c.Resolve == nil || c.Sync == nil {
		return RunResult{}, errors.New("polling coordinator requires state, leases, resolver, and synchronizer")
	}
	if repo.OrganizationID == "" || repo.ID == "" {
		return RunResult{}, errors.New("repository organization and id are required")
	}
	now := c.now()
	ttl := c.LeaseTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	lease, acquired, err := c.Leases.Acquire(ctx, repo.Key(), ttl)
	if err != nil {
		return RunResult{}, fmt.Errorf("acquire repository lease: %w", err)
	}
	if !acquired {
		return RunResult{Outcome: SkippedLease}, nil
	}
	defer lease.Release()
	started := c.now()
	defer func() {
		if c.Observe != nil {
			c.Observe(ctx, repo, result, c.now().Sub(started), retErr)
		}
	}()
	c.audit(ctx, "repository_sync_started", repo, nil)

	status, err := c.State.Get(ctx, repo.Key())
	if err != nil {
		failure := boundedError(err)
		_ = c.State.SaveFailure(ctx, repo.Key(), failure, now)
		c.audit(ctx, "repository_sync_failed", repo, map[string]any{"stage": "state", "error": failure})
		return RunResult{}, fmt.Errorf("load repository state: %w", err)
	}
	commit, err := c.Resolve(ctx, repo)
	if err != nil {
		_ = c.State.SaveFailure(ctx, repo.Key(), boundedError(err), now)
		c.audit(ctx, "repository_sync_failed", repo, map[string]any{"stage": "resolve", "error": boundedError(err)})
		return RunResult{}, err
	}
	if ShouldSkip(status.LastSeenCommit, commit) {
		return RunResult{Outcome: SkippedUnchanged, Commit: commit}, nil
	}
	syncCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var renewalErr chan error
	if renewable, ok := lease.(RenewableLease); ok {
		renewalErr = make(chan error, 1)
		interval := ttl / 3
		if interval < time.Millisecond {
			interval = time.Millisecond
		}
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-syncCtx.Done():
					return
				case <-ticker.C:
					if err := renewable.Renew(syncCtx, ttl); err != nil {
						select {
						case renewalErr <- err:
						default:
						}
						cancel()
						return
					}
				}
			}
		}()
	}
	syncResult, err := c.Sync(syncCtx, repo, commit)
	if renewalErr != nil {
		select {
		case renewErr := <-renewalErr:
			_ = c.State.SaveFailure(ctx, repo.Key(), boundedError(renewErr), c.now())
			c.audit(ctx, "repository_sync_failed", repo, map[string]any{"stage": "renew_lease", "error": boundedError(renewErr)})
			return RunResult{}, renewErr
		default:
		}
	}
	if err != nil {
		_ = c.State.SaveFailure(ctx, repo.Key(), boundedError(err), now)
		c.audit(ctx, "repository_sync_failed", repo, map[string]any{"stage": "sync", "error": boundedError(err)})
		return RunResult{}, err
	}
	if syncResult.Commit == "" {
		syncResult.Commit = commit
	}
	if err := c.State.SaveSuccess(ctx, repo.Key(), syncResult.Commit, c.now()); err != nil {
		c.audit(ctx, "repository_sync_failed", repo, map[string]any{"stage": "state", "error": boundedError(err)})
		return RunResult{}, fmt.Errorf("save repository success: %w", err)
	}
	c.audit(ctx, "repository_sync_succeeded", repo, map[string]any{"commit": syncResult.Commit, "admitted": syncResult.Admitted, "quarantined": syncResult.Quarantined})
	return RunResult{Outcome: Synchronized, Commit: syncResult.Commit, Sync: syncResult}, nil
}

func (c Coordinator) audit(ctx context.Context, event string, repo Repository, details map[string]any) {
	if c.Audit == nil {
		return
	}
	if err := c.Audit(ctx, event, repo, details); err != nil && c.AuditError != nil {
		c.AuditError(ctx, repo, event, err)
	}
}

func (c Coordinator) now() time.Time {
	if c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}

func boundedError(err error) string {
	const max = 1024
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > max {
		return s[:max]
	}
	return s
}

func ShouldSkip(lastCommit, resolvedCommit string) bool {
	return lastCommit != "" && resolvedCommit != "" && lastCommit == resolvedCommit
}

func JitteredInterval(base time.Duration, fraction, sample float64) time.Duration {
	if base <= 0 {
		return 0
	}
	if math.IsNaN(sample) || sample < 0 {
		sample = 0
	}
	if sample > 1 {
		sample = 1
	}
	if fraction < 0 || math.IsNaN(fraction) {
		fraction = 0
	}
	return time.Duration(float64(base) * (1 - fraction + 2*fraction*sample))
}

type Scheduler struct {
	Jitter float64
	Sample func() float64
}

func (s Scheduler) NextDelay(base time.Duration) time.Duration {
	sample := 0.5
	if s.Sample != nil {
		sample = s.Sample()
	}
	return JitteredInterval(base, s.Jitter, sample)
}

// RunRepository performs an initial poll and then continues until ctx is
// cancelled. A failed poll is recorded by Coordinator and does not stop the
// scheduler; the next interval is still calculated from the configured base.
func (s Scheduler) RunRepository(ctx context.Context, repo Repository, coordinator Coordinator) error {
	for {
		_, err := coordinator.RunOnce(ctx, repo)
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		timer := time.NewTimer(s.NextDelay(repo.PollInterval))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type memoryLease struct {
	owner      *memoryLeases
	key, token string
	once       sync.Once
}

func (l *memoryLease) Release() error {
	l.once.Do(func() { l.owner.release(l.key, l.token) })
	return nil
}
func (l *memoryLease) Renew(_ context.Context, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("lease ttl must be positive")
	}
	l.owner.mu.Lock()
	defer l.owner.mu.Unlock()
	current, ok := l.owner.entries[l.key]
	if !ok || current.token != l.token {
		return errors.New("lease ownership lost")
	}
	current.expires = time.Now().Add(ttl)
	l.owner.entries[l.key] = current
	return nil
}

type memoryLeases struct {
	mu      sync.Mutex
	entries map[string]memoryLeaseEntry
	next    uint64
}
type memoryLeaseEntry struct {
	token   string
	expires time.Time
}

func NewMemoryLeases() Leases { return &memoryLeases{entries: make(map[string]memoryLeaseEntry)} }
func (l *memoryLeases) Acquire(_ context.Context, key string, ttl time.Duration) (Lease, bool, error) {
	if key == "" || ttl <= 0 {
		return nil, false, errors.New("lease key and positive ttl are required")
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if current, ok := l.entries[key]; ok && now.Before(current.expires) {
		return nil, false, nil
	}
	l.next++
	token := fmt.Sprintf("%d", l.next)
	l.entries[key] = memoryLeaseEntry{token: token, expires: now.Add(ttl)}
	return &memoryLease{owner: l, key: key, token: token}, true, nil
}
func (l *memoryLeases) release(key, token string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if current, ok := l.entries[key]; ok && current.token == token {
		delete(l.entries, key)
	}
}

type memoryState struct {
	mu     sync.Mutex
	values map[string]Status
}

func NewMemoryState() State { return &memoryState{values: make(map[string]Status)} }
func (s *memoryState) Get(_ context.Context, key string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key], nil
}
func (s *memoryState) SaveSuccess(_ context.Context, key, commit string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.values[key]
	v.LastSeenCommit, v.LastSuccessfulAt, v.LastFailure = commit, at.UTC(), ""
	s.values[key] = v
	return nil
}
func (s *memoryState) SaveFailure(_ context.Context, key, failure string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.values[key]
	v.LastFailedAt, v.LastFailure = at.UTC(), boundedError(errors.New(failure))
	s.values[key] = v
	return nil
}

const sqliteSchema = `CREATE TABLE IF NOT EXISTS polling_sync_state (
 repository_key TEXT PRIMARY KEY,
 last_seen_commit TEXT NOT NULL DEFAULT '',
 last_successful_sync_at TEXT NOT NULL DEFAULT '',
 last_failed_sync_at TEXT NOT NULL DEFAULT '',
 last_failure TEXT NOT NULL DEFAULT ''
); CREATE TABLE IF NOT EXISTS polling_leases (
 repository_key TEXT PRIMARY KEY,
 owner_token TEXT NOT NULL,
 expires_at INTEGER NOT NULL
);`

type SQLiteState struct{ db *sql.DB }

func NewSQLiteState(db *sql.DB) (*SQLiteState, error) {
	if db == nil {
		return nil, errors.New("sqlite database is required")
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		return nil, fmt.Errorf("create polling state: %w", err)
	}
	return &SQLiteState{db: db}, nil
}
func (s *SQLiteState) Get(ctx context.Context, key string) (Status, error) {
	var v Status
	var success, failure string
	err := s.db.QueryRowContext(ctx, `SELECT last_seen_commit,last_successful_sync_at,last_failed_sync_at,last_failure FROM polling_sync_state WHERE repository_key=?`, key).Scan(&v.LastSeenCommit, &success, &failure, &v.LastFailure)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	v.LastSuccessfulAt = parseTime(success)
	v.LastFailedAt = parseTime(failure)
	return v, nil
}
func (s *SQLiteState) SaveSuccess(ctx context.Context, key, commit string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO polling_sync_state(repository_key,last_seen_commit,last_successful_sync_at,last_failed_sync_at,last_failure) VALUES(?,?,?,?,?) ON CONFLICT(repository_key) DO UPDATE SET last_seen_commit=excluded.last_seen_commit,last_successful_sync_at=excluded.last_successful_sync_at,last_failure=''`, key, commit, at.UTC().Format(time.RFC3339Nano), "", "")
	return err
}
func (s *SQLiteState) SaveFailure(ctx context.Context, key, failure string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO polling_sync_state(repository_key,last_failed_sync_at,last_failure) VALUES(?,?,?) ON CONFLICT(repository_key) DO UPDATE SET last_failed_sync_at=excluded.last_failed_sync_at,last_failure=excluded.last_failure`, key, at.UTC().Format(time.RFC3339Nano), boundedError(errors.New(failure)))
	return err
}
func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

type sqliteLease struct {
	db         *sql.DB
	key, token string
	once       sync.Once
}

func (l *sqliteLease) Renew(ctx context.Context, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("lease ttl must be positive")
	}
	now := time.Now()
	result, err := l.db.ExecContext(ctx, `UPDATE polling_leases SET expires_at=? WHERE repository_key=? AND owner_token=? AND expires_at>?`, now.Add(ttl).UnixNano(), l.key, l.token, now.UnixNano())
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("lease ownership lost")
	}
	return nil
}

func (l *sqliteLease) Release() error {
	var err error
	l.once.Do(func() {
		_, err = l.db.Exec(`DELETE FROM polling_leases WHERE repository_key=? AND owner_token=?`, l.key, l.token)
	})
	return err
}

type SQLiteLeases struct {
	db  *sql.DB
	now func() time.Time
}

func NewSQLiteLeases(db *sql.DB, now ...func() time.Time) (Leases, error) {
	if db == nil {
		return nil, errors.New("sqlite database is required")
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		return nil, fmt.Errorf("create polling leases: %w", err)
	}
	clock := time.Now
	if len(now) > 0 && now[0] != nil {
		clock = now[0]
	}
	return &SQLiteLeases{db: db, now: clock}, nil
}
func (l *SQLiteLeases) Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error) {
	if key == "" || ttl <= 0 {
		return nil, false, errors.New("lease key and positive ttl are required")
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, false, err
	}
	token := hex.EncodeToString(tokenBytes)
	now := l.now().UnixNano()
	expiry := l.now().Add(ttl).UnixNano()
	result, err := l.db.ExecContext(ctx, `INSERT INTO polling_leases(repository_key,owner_token,expires_at) VALUES(?,?,?) ON CONFLICT(repository_key) DO UPDATE SET owner_token=excluded.owner_token,expires_at=excluded.expires_at WHERE polling_leases.expires_at <= ?`, key, token, expiry, now)
	if err != nil {
		return nil, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if rows != 1 {
		return nil, false, nil
	}
	return &sqliteLease{db: l.db, key: key, token: token}, true, nil
}
