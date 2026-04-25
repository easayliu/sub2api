//go:build unit

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestBuildSchedulerMetadataAccount_PreservesAccountGroups guards against
// the 2026-04-25 regression: buildSchedulerMetadataAccount previously
// dropped AccountGroups / GroupIDs from the meta cache, causing the
// gateway's isAccountInGroup check (L1.5 g1_inGroup) to return false for
// every grouped API key and triggering an endless delete+SETNX binding
// loop on every request.
func TestBuildSchedulerMetadataAccount_PreservesAccountGroups(t *testing.T) {
	src := service.Account{
		ID:       123,
		Platform: service.PlatformAnthropic,
		Status:   service.StatusActive,
		AccountGroups: []service.AccountGroup{
			{AccountID: 123, GroupID: 14, Priority: 0},
			{AccountID: 123, GroupID: 42, Priority: 1},
		},
		GroupIDs: []int64{14, 42},
	}

	meta := buildSchedulerMetadataAccount(src)

	require.Len(t, meta.AccountGroups, 2)
	require.Equal(t, int64(14), meta.AccountGroups[0].GroupID)
	require.Equal(t, int64(42), meta.AccountGroups[1].GroupID)
	require.Equal(t, []int64{14, 42}, meta.GroupIDs)
}

// TestBuildSchedulerMetadataAccount_NilAccountGroupsNilOut verifies that
// an unlinked account (no group memberships) produces nil slices rather
// than panicking or allocating empty slices — isAccountInGroup relies on
// len==0 as the "ungrouped account" signal.
func TestBuildSchedulerMetadataAccount_NilAccountGroupsNilOut(t *testing.T) {
	src := service.Account{ID: 1, Platform: service.PlatformAnthropic}

	meta := buildSchedulerMetadataAccount(src)

	require.Nil(t, meta.AccountGroups)
	require.Empty(t, meta.GroupIDs)
}

// TestBuildSchedulerMetadataAccount_DeepCopyIsolatesCallers ensures the
// cache copy cannot mutate the caller's slice (or vice versa) — callers
// may still hold references to the source for downstream writes.
func TestBuildSchedulerMetadataAccount_DeepCopyIsolatesCallers(t *testing.T) {
	src := service.Account{
		ID: 1,
		AccountGroups: []service.AccountGroup{
			{AccountID: 1, GroupID: 7},
		},
	}

	meta := buildSchedulerMetadataAccount(src)

	meta.AccountGroups[0].GroupID = 999

	require.Equal(t, int64(7), src.AccountGroups[0].GroupID, "mutating meta copy must not leak back to source")
}
