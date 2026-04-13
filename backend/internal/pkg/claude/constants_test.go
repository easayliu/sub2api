package claude

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuildMessageBetaTokens_OpusMatchesCapture00037 pins the beta token list
// against the real claude-cli/2.1.104 capture (capture/raw/00031), which is
// an opus-4-6 main message with 10 tools and output_config.effort=medium.
//
// Real wire value (from capture):
//
//	claude-code-20250219, oauth-2025-04-20, context-1m-2025-08-07,
//	interleaved-thinking-2025-05-14, redact-thinking-2026-02-12,
//	context-management-2025-06-27, prompt-caching-scope-2026-01-05,
//	advisor-tool-2026-03-01, advanced-tool-use-2025-11-20, effort-2025-11-24
func TestBuildMessageBetaTokens_OpusMatchesCapture00037(t *testing.T) {
	got := BuildMessageBetaTokens(MessageBetaRequestKind{
		ModelID:           "claude-opus-4-6",
		HasTools:          true,
		HasEffort:         true,
		IncludeClaudeCode: true,
		IncludeOAuth:      true,
	})
	want := []string{
		BetaClaudeCode,          // claude-code-20250219
		BetaOAuth,               // oauth-2025-04-20
		BetaContext1M,           // context-1m-2025-08-07
		BetaInterleavedThinking, // interleaved-thinking-2025-05-14
		BetaRedactThinking,      // redact-thinking-2026-02-12
		BetaContextManagement,   // context-management-2025-06-27
		BetaPromptCachingScope,  // prompt-caching-scope-2026-01-05
		BetaAdvisorTool,         // advisor-tool-2026-03-01
		BetaAdvancedToolUse,     // advanced-tool-use-2025-11-20
		BetaEffort,              // effort-2025-11-24
	}
	require.Equal(t, want, got)
}

// TestBuildMessageBetaTokens_HaikuStructuredMatchesCapture011 pins the haiku
// path against capture/011 (haiku title generation with structured output).
//
// Real wire value (from capture):
//
//	oauth-2025-04-20, interleaved-thinking-2025-05-14, redact-thinking-2026-02-12,
//	context-management-2025-06-27, prompt-caching-scope-2026-01-05,
//	advisor-tool-2026-03-01, structured-outputs-2025-12-15
func TestBuildMessageBetaTokens_HaikuStructuredMatchesCapture011(t *testing.T) {
	got := BuildMessageBetaTokens(MessageBetaRequestKind{
		ModelID:           "claude-haiku-4-5-20251001",
		HasTools:          false,
		HasStructuredOut:  true,
		IncludeClaudeCode: true, // ignored for haiku
		IncludeOAuth:      true,
	})
	want := []string{
		BetaOAuth,               // oauth-2025-04-20
		BetaInterleavedThinking, // interleaved-thinking-2025-05-14
		BetaRedactThinking,      // redact-thinking-2026-02-12
		BetaContextManagement,   // context-management-2025-06-27
		BetaPromptCachingScope,  // prompt-caching-scope-2026-01-05
		BetaAdvisorTool,         // advisor-tool-2026-03-01
		BetaStructuredOutputs,   // structured-outputs-2025-12-15
	}
	require.Equal(t, want, got)
}

// TestBuildMessageBetaTokens_HaikuQuotaProbeMatchesCapture008 pins the
// minimal haiku quota-probe shape against capture/008.
func TestBuildMessageBetaTokens_HaikuQuotaProbeMatchesCapture008(t *testing.T) {
	got := BuildMessageBetaTokens(MessageBetaRequestKind{
		ModelID:           "claude-haiku-4-5-20251001",
		IsQuotaProbe:      true,
		IncludeClaudeCode: true,
		IncludeOAuth:      true,
	})
	want := []string{
		BetaOAuth,
		BetaInterleavedThinking,
		BetaRedactThinking,
		BetaContextManagement,
		BetaPromptCachingScope,
		BetaAdvisorTool,
	}
	require.Equal(t, want, got)
}

// TestBuildMessageBetaTokens_NonHaikuWithoutToolsOrEffort verifies that
// advanced-tool-use and effort are correctly omitted when their gating
// fields are false.
func TestBuildMessageBetaTokens_NonHaikuWithoutToolsOrEffort(t *testing.T) {
	got := BuildMessageBetaTokens(MessageBetaRequestKind{
		ModelID:           "claude-opus-4-6",
		IncludeClaudeCode: true,
		IncludeOAuth:      true,
	})
	require.NotContains(t, got, BetaAdvancedToolUse)
	require.NotContains(t, got, BetaEffort)
	require.Contains(t, got, BetaClaudeCode)
	require.Contains(t, got, BetaContext1M)
}

// TestBuildMessageBetaTokens_CountTokensAppendsTokenCounting verifies that
// the count_tokens endpoint variant adds the token-counting beta at the end.
func TestBuildMessageBetaTokens_CountTokensAppendsTokenCounting(t *testing.T) {
	got := BuildMessageBetaTokens(MessageBetaRequestKind{
		ModelID:            "claude-opus-4-6",
		HasTools:           true,
		IncludeClaudeCode:  true,
		IncludeOAuth:       true,
		IncludeTokenCounts: true,
	})
	require.Equal(t, BetaTokenCounting, got[len(got)-1])
}

// TestBuildMessageBetaTokens_APIKeyVariant verifies that omitting OAuth
// produces a list without the oauth beta (used by API-key accounts).
func TestBuildMessageBetaTokens_APIKeyVariant(t *testing.T) {
	got := BuildMessageBetaTokens(MessageBetaRequestKind{
		ModelID:           "claude-opus-4-6",
		IncludeClaudeCode: true,
		IncludeOAuth:      false,
	})
	require.NotContains(t, got, BetaOAuth)
	require.Contains(t, got, BetaClaudeCode)
}

// TestDefaultHeaders_Version104 pins the default User-Agent to 2.1.104
// so a future accidental downgrade is caught.
func TestDefaultHeaders_Version104(t *testing.T) {
	ua := DefaultHeaders["User-Agent"]
	require.True(t, strings.Contains(ua, "2.1.104"),
		"User-Agent should advertise 2.1.104, got %q", ua)
	require.True(t, strings.Contains(ua, "(external, claude-desktop)"),
		"User-Agent should keep the (external, claude-desktop) suffix, got %q", ua)
}
