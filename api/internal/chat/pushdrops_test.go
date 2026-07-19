package chat

import (
	"sync"
	"testing"
	"time"
)

// The onset of dropping is the event an operator needs to see, so it must not wait for a window to
// close. A summariser that batches the first drop hides the transition from working to dropping for
// up to half a minute, which is exactly the half-minute someone is trying to explain.
func TestDropCounter_ReportsTheFirstDropImmediately(t *testing.T) {
	var d dropCounter

	n, report := d.record(time.Now())
	if !report {
		t.Fatal("the first dropped notification was not reported; the onset of dropping is invisible")
	}
	if n != 1 {
		t.Errorf("the first drop reported a count of %d, want 1", n)
	}
}

// Between reports it must stay quiet — the entire point of the change.
func TestDropCounter_StaysQuietWithinTheWindow(t *testing.T) {
	var d dropCounter
	start := time.Now()

	if _, report := d.record(start); !report {
		t.Fatal("setup: the first drop was not reported")
	}
	for i := 0; i < 500; i++ {
		if _, report := d.record(start.Add(time.Duration(i) * time.Millisecond)); report {
			t.Fatalf("a second line was written %dms into the window; at the ceiling this is the "+
				"hundreds of lines the summary exists to prevent", i)
		}
	}
}

// And when the window closes, the summary must account for everything that happened during it.
func TestDropCounter_SummarisesTheWholeWindow(t *testing.T) {
	var d dropCounter
	start := time.Now()

	d.record(start) // reported immediately, count 1
	const during = 99
	for i := 0; i < during; i++ {
		d.record(start.Add(time.Second))
	}

	n, report := d.record(start.Add(reportWindow))
	if !report {
		t.Fatal("nothing was reported after the window closed; drops continue unseen")
	}
	// The 99 silent ones plus the one that triggered this report. Not 99, and not 100 plus the
	// first — an operator summing these lines must get the true total.
	if n != during+1 {
		t.Errorf("the summary reported %d drops, want %d; every reported count must sum to the "+
			"real total, or the number an operator alerts on is wrong", n, during+1)
	}
}

// The property that makes the summary trustworthy: across a whole run, the reported counts plus
// whatever is still pending equal every drop that happened. Nothing counted twice, nothing lost.
func TestDropCounter_LosesNoDropAcrossManyWindows(t *testing.T) {
	var d dropCounter
	start := time.Now()

	const total = 1000
	reported := 0
	for i := 0; i < total; i++ {
		// Spread across several windows, so this exercises the reset rather than one report.
		at := start.Add(time.Duration(i) * (reportWindow / 100))
		if n, report := d.record(at); report {
			reported += n
		}
	}

	d.mu.Lock()
	pending := d.pending
	d.mu.Unlock()

	if reported+pending != total {
		t.Errorf("%d drops were reported and %d are pending, which accounts for %d of %d actual "+
			"drops", reported, pending, reported+pending, total)
	}
}

// Drops happen on the request path, from every goroutine serving a send, so the counter is
// contended by construction. Run under -race: a lost increment here understates a problem an
// operator is trying to size.
func TestDropCounter_CountsEveryDropUnderConcurrency(t *testing.T) {
	var d dropCounter

	const goroutines, each = 50, 200
	var mu sync.Mutex
	reported := 0

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if n, report := d.record(time.Now()); report {
					mu.Lock()
					reported += n
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	d.mu.Lock()
	pending := d.pending
	d.mu.Unlock()

	if want := goroutines * each; reported+pending != want {
		t.Errorf("%d concurrent drops were accounted for, want %d — %d were lost",
			reported+pending, want, want-(reported+pending))
	}
}
