package store

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentWritersSerializeWithoutError drives parallel writers across
// every repository. SQLite serializes writes; with WAL and the busy timeout
// the store must absorb the contention without SQLITE_BUSY errors or panics.
// Run under -race in the B8 gate.
func TestConcurrentWritersSerializeWithoutError(t *testing.T) {
	s := openStore(t, t.TempDir())
	seedProject(t, s, "p1")

	const workers = 8
	const perWorker = 20

	errs := make(chan error, workers*perWorker*4)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := s.AppendAudit(AuditRow{
					Kind: "spawn", ProjectKey: "p1", Epoch: 1,
					AtMS: int64(w*1000 + i),
				}); err != nil {
					errs <- fmt.Errorf("worker %d audit %d: %w", w, i, err)
				}
				if err := s.InsertNotification(NotificationRow{
					ID:      fmt.Sprintf("n-%d-%d", w, i),
					Session: "s1", CreatedMS: int64(w*1000 + i),
				}); err != nil {
					errs <- fmt.Errorf("worker %d notify %d: %w", w, i, err)
				}
				if err := s.SetCursor(fmt.Sprintf("c%d", w), "s1", uint64(i)); err != nil {
					errs <- fmt.Errorf("worker %d cursor %d: %w", w, i, err)
				}
				if err := s.SetMeta(fmt.Sprintf("k%d", w), fmt.Sprintf("v%d", i)); err != nil {
					errs <- fmt.Errorf("worker %d meta %d: %w", w, i, err)
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}

	if n, err := s.CountAudit(); err != nil || n != workers*perWorker {
		t.Fatalf("CountAudit = %d, %v; want %d", n, err, workers*perWorker)
	}
	if n, err := s.CountUnread("s1"); err != nil || n != workers*perWorker {
		t.Fatalf("CountUnread = %d, %v; want %d", n, err, workers*perWorker)
	}
}
