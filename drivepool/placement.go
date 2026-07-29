package drivepool

import (
	"context"
	"errors"
	"sort"
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
