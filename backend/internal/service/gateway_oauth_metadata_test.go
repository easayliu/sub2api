package service

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildOAuthMetadataUserID_FallbackWithoutAccountUUID(t *testing.T) {
	svc := &GatewayService{}

	parsed := &ParsedRequest{
		Model:          "claude-sonnet-4-5",
		Stream:         true,
		MetadataUserID: "",
		System:         nil,
		Messages:       nil,
	}

	account := &Account{
		ID:    123,
		Type:  AccountTypeOAuth,
		Extra: map[string]any{}, // intentionally missing account_uuid / claude_user_id
	}

	fp := &Fingerprint{ClientID: "deadbeef"} // should be used as user id in legacy format

	got := svc.buildOAuthMetadataUserID(parsed, account, fp)
	require.NotEmpty(t, got)

	// Legacy format: user_{client}_account__session_{uuid}
	re := regexp.MustCompile(`^user_[a-zA-Z0-9]+_account__session_[a-f0-9-]{36}$`)
	require.True(t, re.MatchString(got), "unexpected user_id format: %s", got)
}

func TestBuildOAuthMetadataUserID_UsesAccountUUIDWhenPresent(t *testing.T) {
	svc := &GatewayService{}

	parsed := &ParsedRequest{
		Model:          "claude-sonnet-4-5",
		Stream:         true,
		MetadataUserID: "",
	}

	account := &Account{
		ID:   123,
		Type: AccountTypeOAuth,
		Extra: map[string]any{
			"account_uuid":      "acc-uuid",
			"claude_user_id":    "clientid123",
			"anthropic_user_id": "",
		},
	}

	got := svc.buildOAuthMetadataUserID(parsed, account, nil)
	require.NotEmpty(t, got)

	// New format: user_{client}_account_{account_uuid}_session_{uuid}
	re := regexp.MustCompile(`^user_clientid123_account_acc-uuid_session_[a-f0-9-]{36}$`)
	require.True(t, re.MatchString(got), "unexpected user_id format: %s", got)
}

// TestBuildOAuthMetadataUserID_SessionIDStableAcrossTurns verifies that the
// derived session_id stays constant across multiple turns of one
// conversation, matching real claude-cli's process-scoped session token.
//
// This is the regression guard for the GMT 03:00 risk-control batch
// detection issue: previously the session_id was derived from
// GenerateSessionHash, which intentionally drifts per turn for
// sticky-session routing. That made every turn look like a new session
// from Anthropic's view. The fix uses buildStableConversationSeed which
// hashes only conversation-stable inputs.
func TestBuildOAuthMetadataUserID_SessionIDStableAcrossTurns(t *testing.T) {
	svc := &GatewayService{}

	account := &Account{
		ID:   42,
		Type: AccountTypeOAuth,
		Extra: map[string]any{
			"account_uuid":   "acc-uuid",
			"claude_user_id": "clientid123",
		},
	}

	turn1 := &ParsedRequest{
		Model:     "claude-sonnet-4-5",
		System:    "You are a helpful assistant.",
		HasSystem: true,
		Messages: []any{
			map[string]any{"role": "user", "content": "What is the capital of France?"},
		},
	}
	turn2 := &ParsedRequest{
		Model:     "claude-sonnet-4-5",
		System:    "You are a helpful assistant.",
		HasSystem: true,
		Messages: []any{
			map[string]any{"role": "user", "content": "What is the capital of France?"},
			map[string]any{"role": "assistant", "content": "Paris."},
			map[string]any{"role": "user", "content": "What about Germany?"},
		},
	}
	turn3 := &ParsedRequest{
		Model:     "claude-sonnet-4-5",
		System:    "You are a helpful assistant.",
		HasSystem: true,
		Messages: []any{
			map[string]any{"role": "user", "content": "What is the capital of France?"},
			map[string]any{"role": "assistant", "content": "Paris."},
			map[string]any{"role": "user", "content": "What about Germany?"},
			map[string]any{"role": "assistant", "content": "Berlin."},
			map[string]any{"role": "user", "content": "And Spain?"},
		},
	}

	id1 := svc.buildOAuthMetadataUserID(turn1, account, nil)
	id2 := svc.buildOAuthMetadataUserID(turn2, account, nil)
	id3 := svc.buildOAuthMetadataUserID(turn3, account, nil)
	require.NotEmpty(t, id1)
	require.Equal(t, id1, id2, "session_id must be stable across turns of one conversation")
	require.Equal(t, id2, id3, "session_id must be stable across turns of one conversation")

	// Different conversation (different first user message) MUST produce a
	// different session_id, otherwise unrelated chats would collapse onto
	// the same upstream session.
	otherConversation := &ParsedRequest{
		Model:     "claude-sonnet-4-5",
		System:    "You are a helpful assistant.",
		HasSystem: true,
		Messages: []any{
			map[string]any{"role": "user", "content": "Write a Go function that reverses a string."},
		},
	}
	idOther := svc.buildOAuthMetadataUserID(otherConversation, account, nil)
	require.NotEmpty(t, idOther)
	require.NotEqual(t, id1, idOther, "different first user message must produce a different session_id")

	// Different account MUST produce a different session_id even for the
	// exact same conversation, so accounts cannot collide.
	otherAccount := &Account{
		ID:   99,
		Type: AccountTypeOAuth,
		Extra: map[string]any{
			"account_uuid":   "acc-uuid",
			"claude_user_id": "clientid123",
		},
	}
	idOtherAcct := svc.buildOAuthMetadataUserID(turn1, otherAccount, nil)
	require.NotEmpty(t, idOtherAcct)
	require.NotEqual(t, id1, idOtherAcct, "same conversation under different accounts must not collide")
}
