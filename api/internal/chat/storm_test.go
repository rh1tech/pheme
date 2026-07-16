package chat

import (
	"testing"
	"time"
)

func TestStormDetectorCrossesThresholdExactlyOnce(t *testing.T) {
	d := newCommitStormDetector(10*time.Minute, 5)
	now := time.Now()

	fired := 0
	for i := 0; i < 20; i++ {
		if _, crossed := d.Observe("conv", now.Add(time.Duration(i)*time.Second)); crossed {
			fired++
			if i != 4 { // the fifth Commit is the one that crosses
				t.Fatalf("crossed on commit %d, want commit 5", i+1)
			}
		}
	}
	if fired != 1 {
		t.Fatalf("threshold fired %d times in one window, want exactly once", fired)
	}
}

func TestStormDetectorResetsAfterTheWindow(t *testing.T) {
	d := newCommitStormDetector(time.Minute, 3)
	now := time.Now()

	for i := 0; i < 3; i++ {
		d.Observe("conv", now)
	}
	// A new window: the count starts over, and the alarm is re-armed.
	later := now.Add(2 * time.Minute)
	if n, crossed := d.Observe("conv", later); crossed || n != 1 {
		t.Fatalf("after window rollover got n=%d crossed=%v, want n=1 crossed=false", n, crossed)
	}
	d.Observe("conv", later)
	if _, crossed := d.Observe("conv", later); !crossed {
		t.Fatal("a storm in a fresh window must fire again")
	}
}

func TestStormDetectorTracksConversationsIndependently(t *testing.T) {
	d := newCommitStormDetector(time.Minute, 3)
	now := time.Now()

	d.Observe("a", now)
	d.Observe("a", now)
	if _, crossed := d.Observe("b", now); crossed {
		t.Fatal("conversation b inherited conversation a's count")
	}
}
