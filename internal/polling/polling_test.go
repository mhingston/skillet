package polling

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/store"
)

func TestShouldSyncSkipsOnlyAnIdenticalNonEmptyCommit(t *testing.T) {
	tests := []struct {
		name           string
		last, resolved string
		wantSkip       bool
	}{
		{"same", "abc", "abc", true},
		{"changed", "abc", "def", false},
		{"first poll", "", "abc", false},
		{"unknown resolution", "abc", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldSkip(tt.last, tt.resolved); got != tt.wantSkip {
				t.Fatalf("ShouldSkip(%q, %q) = %v, want %v", tt.last, tt.resolved, got, tt.wantSkip)
			}
		})
	}
}

func TestJitteredIntervalIsBoundedAndDeterministic(t *testing.T) {
	base := 10 * time.Minute
	if got := JitteredInterval(base, 0.1, 0); got != 9*time.Minute {
		t.Fatalf("low jitter = %s", got)
	}
	if got := JitteredInterval(base, 0.1, 1); got != 11*time.Minute {
		t.Fatalf("high jitter = %s", got)
	}
	if got := JitteredInterval(base, 0.1, 0.5); got != base {
		t.Fatalf("mid jitter = %s", got)
	}
	if got := JitteredInterval(base, 0.5, -1); got != 5*time.Minute {
		t.Fatalf("clamped low jitter = %s", got)
	}
	if got := JitteredInterval(base, 0.5, 2); got != 15*time.Minute {
		t.Fatalf("clamped high jitter = %s", got)
	}
}

func TestMemoryLeaseIsRepositoryScopedAndExclusive(t *testing.T) {
	leases := NewMemoryLeases()
	first, acquired, err := leases.Acquire(context.Background(), "org/repo", time.Minute)
	if err != nil || !acquired || first == nil {
		t.Fatalf("first acquire = (%v, %v, %v)", first, acquired, err)
	}
	second, acquired, err := leases.Acquire(context.Background(), "org/repo", time.Minute)
	if err != nil || acquired || second != nil {
		t.Fatalf("second acquire = (%v, %v, %v)", second, acquired, err)
	}
	other, acquired, err := leases.Acquire(context.Background(), "org/other", time.Minute)
	if err != nil || !acquired || other == nil {
		t.Fatalf("other acquire = (%v, %v, %v)", other, acquired, err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	third, acquired, err := leases.Acquire(context.Background(), "org/repo", time.Minute)
	if err != nil || !acquired || third == nil {
		t.Fatalf("acquire after release = (%v, %v, %v)", third, acquired, err)
	}
}

func TestCoordinatorSkipsUnchangedAndPersistsSuccessAndFailure(t *testing.T) {
	state := NewMemoryState()
	leases := NewMemoryLeases()
	var syncCalls int
	coord := Coordinator{
		State: state, Leases: leases, LeaseTTL: time.Minute,
		Resolve: func(context.Context, Repository) (string, error) { return "abc", nil },
		Sync: func(context.Context, Repository, string) (SyncResult, error) {
			syncCalls++
			return SyncResult{Commit: "abc", Admitted: 2}, nil
		},
	}
	repo := Repository{OrganizationID: "org", ID: "repo"}
	first, err := coord.RunOnce(context.Background(), repo)
	if err != nil || first.Outcome != Synchronized || syncCalls != 1 {
		t.Fatalf("first run = %+v, calls=%d, err=%v", first, syncCalls, err)
	}
	second, err := coord.RunOnce(context.Background(), repo)
	if err != nil || second.Outcome != SkippedUnchanged || syncCalls != 1 {
		t.Fatalf("second run = %+v, calls=%d, err=%v", second, syncCalls, err)
	}
	coord.Resolve = func(context.Context, Repository) (string, error) { return "def", nil }
	coord.Sync = func(context.Context, Repository, string) (SyncResult, error) {
		return SyncResult{}, errors.New("fetch failed")
	}
	if _, err := coord.RunOnce(context.Background(), repo); err == nil {
		t.Fatal("failed sync returned nil error")
	}
	status, err := state.Get(context.Background(), repo.Key())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastSeenCommit != "abc" || status.LastFailure != "fetch failed" || status.LastSuccessfulAt.IsZero() || status.LastFailedAt.IsZero() {
		t.Fatalf("status = %+v", status)
	}
}

func TestCoordinatorReportsSyncDurationOutcomeAndError(t *testing.T) {
	state := NewMemoryState()
	leases := NewMemoryLeases()
	type observation struct {
		outcome  Outcome
		duration time.Duration
		err      error
	}
	var observations []observation
	coord := Coordinator{
		State: state, Leases: leases, LeaseTTL: time.Minute,
		Resolve: func(context.Context, Repository) (string, error) { return "abc", nil },
		Sync:    func(context.Context, Repository, string) (SyncResult, error) { return SyncResult{Commit: "abc"}, nil },
		Observe: func(_ context.Context, _ Repository, result RunResult, duration time.Duration, err error) {
			observations = append(observations, observation{result.Outcome, duration, err})
		},
	}
	if _, err := coord.RunOnce(context.Background(), Repository{OrganizationID: "org", ID: "repo"}); err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].outcome != Synchronized || observations[0].duration <= 0 || observations[0].err != nil {
		t.Fatalf("observations = %+v", observations)
	}
}

func TestCoordinatorDoesNotOverlapSameRepository(t *testing.T) {
	coord := Coordinator{
		State: NewMemoryState(), Leases: NewMemoryLeases(), LeaseTTL: time.Minute,
		Resolve: func(context.Context, Repository) (string, error) { return "a", nil },
		Sync: func(context.Context, Repository, string) (SyncResult, error) {
			time.Sleep(20 * time.Millisecond)
			return SyncResult{Commit: "a"}, nil
		},
	}
	repo := Repository{OrganizationID: "org", ID: "repo"}
	var wg sync.WaitGroup
	results := make(chan Outcome, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, _ := coord.RunOnce(context.Background(), repo)
			results <- result.Outcome
		}()
	}
	wg.Wait()
	close(results)
	var synchronized, busy int
	for outcome := range results {
		if outcome == Synchronized {
			synchronized++
		}
		if outcome == SkippedLease {
			busy++
		}
	}
	if synchronized != 1 || busy != 1 {
		t.Fatalf("outcomes synchronized=%d busy=%d", synchronized, busy)
	}
}

func TestCoordinatorRenewsLongRunningLease(t *testing.T) {
	const ttl = 45 * time.Millisecond
	base := NewMemoryLeases()
	renewed := make(chan struct{}, 8)
	leases := &observingLeases{base: base, renewed: renewed}
	finish := make(chan struct{})
	started := make(chan struct{})
	coord := Coordinator{
		State: NewMemoryState(), Leases: leases, LeaseTTL: ttl,
		Resolve: func(context.Context, Repository) (string, error) { return "a", nil },
		Sync: func(ctx context.Context, _ Repository, _ string) (SyncResult, error) {
			close(started)
			select {
			case <-finish:
				return SyncResult{Commit: "a"}, nil
			case <-ctx.Done():
				return SyncResult{}, ctx.Err()
			}
		},
	}
	repo := Repository{OrganizationID: "org", ID: "repo"}
	resultCh := make(chan struct {
		result RunResult
		err    error
	}, 1)
	go func() {
		result, err := coord.RunOnce(context.Background(), repo)
		resultCh <- struct {
			result RunResult
			err    error
		}{result: result, err: err}
	}()
	<-started
	select {
	case <-renewed:
	case <-time.After(time.Second):
		t.Fatal("lease was not renewed before its TTL elapsed")
	}
	select {
	case <-renewed:
	case <-time.After(time.Second):
		t.Fatal("lease was not renewed a second time")
	}

	competing, err := coord.RunOnce(context.Background(), repo)
	if err != nil {
		t.Fatalf("competing run error = %v", err)
	}
	if competing.Outcome != SkippedLease {
		t.Fatalf("competing run outcome = %q, want %q", competing.Outcome, SkippedLease)
	}
	close(finish)
	completed := <-resultCh
	if completed.err != nil || completed.result.Outcome != Synchronized {
		t.Fatalf("long-running run = %+v, err=%v", completed.result, completed.err)
	}
}

func TestCoordinatorCancelsSyncWhenLeaseRenewalFails(t *testing.T) {
	lease := &failingRenewLease{err: errors.New("lease ownership lost")}
	coord := Coordinator{
		State: NewMemoryState(), Leases: fixedLease{lease: lease}, LeaseTTL: 30 * time.Millisecond,
		Resolve: func(context.Context, Repository) (string, error) { return "a", nil },
		Sync: func(ctx context.Context, _ Repository, _ string) (SyncResult, error) {
			<-ctx.Done()
			return SyncResult{}, ctx.Err()
		},
	}
	repo := Repository{OrganizationID: "org", ID: "repo"}
	_, err := coord.RunOnce(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "lease ownership lost") {
		t.Fatalf("renewal failure = %v, want lease ownership error", err)
	}
	status, err := coord.State.Get(context.Background(), repo.Key())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastSeenCommit != "" || status.LastFailure == "" {
		t.Fatalf("status after renewal failure = %+v", status)
	}
}

type observingLeases struct {
	base    Leases
	renewed chan<- struct{}
}

func (l *observingLeases) Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error) {
	lease, acquired, err := l.base.Acquire(ctx, key, ttl)
	if err != nil || !acquired {
		return lease, acquired, err
	}
	return &observingLease{Lease: lease, renewed: l.renewed}, true, nil
}

type observingLease struct {
	Lease
	renewed chan<- struct{}
}

func (l *observingLease) Renew(ctx context.Context, ttl time.Duration) error {
	err := l.Lease.(RenewableLease).Renew(ctx, ttl)
	if err == nil {
		l.renewed <- struct{}{}
	}
	return err
}

type fixedLease struct{ lease Lease }

func (l fixedLease) Acquire(context.Context, string, time.Duration) (Lease, bool, error) {
	return l.lease, true, nil
}

type failingRenewLease struct{ err error }

func (l *failingRenewLease) Release() error { return nil }
func (l *failingRenewLease) Renew(context.Context, time.Duration) error {
	return l.err
}

func TestSQLiteStateAndLeasesSurviveNewHandles(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, err := NewSQLiteState(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	if err := state.SaveSuccess(context.Background(), "org/repo", "commit", now); err != nil {
		t.Fatal(err)
	}
	status, err := state.Get(context.Background(), "org/repo")
	if err != nil || status.LastSeenCommit != "commit" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	leases, err := NewSQLiteLeases(db, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := leases.Acquire(context.Background(), "org/repo", time.Minute)
	if err != nil || !acquired || lease == nil {
		t.Fatalf("acquire=(%v,%v,%v)", lease, acquired, err)
	}
	other, err := NewSQLiteLeases(db, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	_, acquired, err = other.Acquire(context.Background(), "org/repo", time.Minute)
	if err != nil || acquired {
		t.Fatalf("second acquire=(%v,%v)", acquired, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerRunOnceUsesConfiguredJitter(t *testing.T) {
	s := Scheduler{Jitter: 0.1, Sample: func() float64 { return 0 }}
	if got := s.NextDelay(10 * time.Minute); got != 9*time.Minute {
		t.Fatalf("NextDelay = %s", got)
	}
}

var _ *sql.DB
