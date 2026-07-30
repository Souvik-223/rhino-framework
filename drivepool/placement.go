package drivepool

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var ErrNoHealthyAccounts = errors.New("drivepool: no healthy accounts with known free space")

// pickAccount implements the "least-used-first" / "most-free-space-first"
// placement strategy: it queries live quota for every healthy account and
// returns whichever currently has the most available bytes. This mirrors
// HDFS's default preference for placing blocks on datanodes with more free
// space, and is deliberately re-queried per call rather than cached, since
// callers place at most a handful of chunks per operation today.
func pickAccount(ctx context.Context, accounts []*Account) (*Account, error) {
	type scored struct {
		acc   *Account
		avail int64
	}

	var candidates []scored
	for _, a := range accounts {
		if a.initErr != nil {
			continue
		}
		q, err := a.Store.Quota(ctx)
		if err != nil {
			continue
		}
		candidates = append(candidates, scored{acc: a, avail: q.Available()})
	}

	if len(candidates) == 0 {
		return nil, ErrNoHealthyAccounts
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].avail > candidates[j].avail })
	return candidates[0].acc, nil
}

// placementTracker places many chunks of one file across accounts without
// re-querying live quota per chunk. pickAccount deliberately re-queries on
// every call, which is fine for "once per whole file" but wrong for "once
// per chunk, from several goroutines at once": redundant network round
// trips, and a real race where two goroutines could both see the same
// account as most-free before either upload actually registers. Instead,
// live quota is queried once per healthy account up front, then every
// chunk's placement decision is an in-memory, mutex-guarded lookup.
type placementTracker struct {
	mu        sync.Mutex
	available map[string]int64 // accountID -> remaining budget, decremented as chunks are reserved
	unlimited map[string]bool  // accountID -> true if its quota is unlimited
}

func newPlacementTracker(ctx context.Context, accounts []*Account) (*placementTracker, error) {
	t := &placementTracker{
		available: make(map[string]int64),
		unlimited: make(map[string]bool),
	}

	healthy := 0
	for _, a := range accounts {
		if a.initErr != nil {
			continue
		}
		q, err := a.Store.Quota(ctx)
		if err != nil {
			continue
		}
		healthy++
		if q.Unlimited {
			t.unlimited[a.ID] = true
		} else {
			t.available[a.ID] = q.Available()
		}
	}

	if healthy == 0 {
		return nil, ErrNoHealthyAccounts
	}
	return t, nil
}

// reserve picks an account for a chunk of the given size and deducts size
// from that account's tracked budget, so the next reserve call sees the
// updated remaining space without another network round trip.
func (t *placementTracker) reserve(size int64) (accountID string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Any unlimited account works — none of them meaningfully run out, so
	// there's no capacity-driven reason to balance across them.
	for id := range t.unlimited {
		return id, nil
	}

	bestID, bestAvail := "", int64(-1)
	for id, avail := range t.available {
		if avail > bestAvail {
			bestID, bestAvail = id, avail
		}
	}
	if bestID == "" {
		return "", ErrNoHealthyAccounts
	}
	t.available[bestID] -= size
	return bestID, nil
}
