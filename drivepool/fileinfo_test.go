package drivepool

import (
	"bytes"
	"context"
	"sort"
	"testing"
)

func TestListWithAccountsReportsEveryDriveHoldingAChunk(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	p.ChunkSize = 10

	addFakeAccount(t, p, "acct-a", "home-gmail", 25, 0)
	addFakeAccount(t, p, "acct-b", "work-gmail", 20, 0)

	data := bytes.Repeat([]byte("z"), 40) // 4 chunks, spread across both accounts
	if err := p.PutStream(ctx, bytes.NewReader(data), int64(len(data)), "spread.bin"); err != nil {
		t.Fatalf("PutStream: %v", err)
	}

	files, err := p.ListWithAccounts(ctx, "")
	if err != nil {
		t.Fatalf("ListWithAccounts: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}

	got := append([]string(nil), files[0].Accounts...)
	sort.Strings(got)
	want := []string{"home-gmail", "work-gmail"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("want accounts %v, got %v", want, got)
	}
}

func TestListWithAccountsSingleChunkReportsOneDrive(t *testing.T) {
	ctx := context.Background()
	p, _ := newTestPool(t)
	addFakeAccount(t, p, "acct-a", "only-drive", 1<<20, 0)

	if err := p.PutStream(ctx, bytes.NewReader([]byte("small")), 5, "small.txt"); err != nil {
		t.Fatalf("PutStream: %v", err)
	}

	files, err := p.ListWithAccounts(ctx, "")
	if err != nil {
		t.Fatalf("ListWithAccounts: %v", err)
	}
	if len(files) != 1 || len(files[0].Accounts) != 1 || files[0].Accounts[0] != "only-drive" {
		t.Errorf("want exactly [\"only-drive\"], got %+v", files)
	}
}
