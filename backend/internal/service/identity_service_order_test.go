package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type identityCacheStub struct {
	maskedSessionID string
}

func (s *identityCacheStub) GetFingerprint(_ context.Context, _ int64) (*Fingerprint, error) {
	return nil, nil
}
func (s *identityCacheStub) SetFingerprint(_ context.Context, _ int64, _ *Fingerprint) error {
	return nil
}
func (s *identityCacheStub) GetMaskedSessionID(_ context.Context, _ int64) (string, error) {
	return s.maskedSessionID, nil
}
func (s *identityCacheStub) SetMaskedSessionID(_ context.Context, _ int64, sessionID string) error {
	s.maskedSessionID = sessionID
	return nil
}

func TestIdentityService_RewriteUserID_PreservesTopLevelFieldOrder(t *testing.T) {
	cache := &identityCacheStub{}
	svc := NewIdentityService(cache)

	originalUserID := FormatMetadataUserID(
		"d61f76d0730d2b920763648949bad5c79742155c27037fc77ac3f9805cb90169",
		"",
		"7578cf37-aaca-46e4-a45c-71285d9dbb83",
		"2.1.78",
	)
	body := []byte(`{"alpha":1,"messages":[],"metadata":{"user_id":` + strconvQuote(originalUserID) + `},"max_tokens":64000,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"stream":true}`)

	result, err := svc.RewriteUserID(body, 123, "acc-uuid", "client-xyz", "claude-cli/2.1.78 (external, cli)")
	require.NoError(t, err)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"messages"`, `"metadata"`, `"max_tokens"`, `"thinking"`, `"output_config"`, `"stream"`)
	// device_id 和 account_uuid 仍被改写 -> 整体 originalUserID 字符串不再出现
	require.NotContains(t, resultStr, originalUserID)
	require.Contains(t, resultStr, `"metadata":{"user_id":"`)
}

// TestRewriteUserID_PreservesClientSessionID pins the design decision that
// metadata.user_id.session_id keeps the client-supplied value, while only
// device_id and account_uuid are rewritten to the account-level identity.
//
// Reasoning: account isolation is fully achieved through device_id and
// account_uuid; session_id derivation is redundant and breaks the
// header == metadata.user_id.session_id invariant observed in real
// claude-cli/2.1.100 traffic.
func TestRewriteUserID_PreservesClientSessionID(t *testing.T) {
	svc := NewIdentityService(&identityCacheStub{})

	const (
		clientDeviceID  = "d61f76d0730d2b920763648949bad5c79742155c27037fc77ac3f9805cb90169"
		clientSessionID = "7578cf37-aaca-46e4-a45c-71285d9dbb83"
		cachedClientID  = "abcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabca"
		accountUUID     = "11111111-2222-4333-8444-555555555555"
	)
	originalUserID := FormatMetadataUserID(clientDeviceID, "", clientSessionID, "2.1.100")
	body := []byte(`{"messages":[],"metadata":{"user_id":` + strconvQuote(originalUserID) + `}}`)

	result, err := svc.RewriteUserID(body, 999, accountUUID, cachedClientID, "claude-cli/2.1.100 (external, cli)")
	require.NoError(t, err)

	rewrittenUserID := gjson.GetBytes(result, "metadata.user_id").String()
	require.NotEmpty(t, rewrittenUserID)

	parsed := ParseMetadataUserID(rewrittenUserID)
	require.NotNil(t, parsed)

	require.Equal(t, cachedClientID, parsed.DeviceID, "device_id must be rewritten to cachedClientID")
	require.Equal(t, accountUUID, parsed.AccountUUID, "account_uuid must be rewritten to account UUID")
	require.Equal(t, clientSessionID, parsed.SessionID, "session_id must keep the client-supplied value")
}

// TestRewriteUserID_LegacyFormatPreservesSessionID verifies the same invariant
// for legacy concatenated user_id format (claude-cli < 2.1.78).
func TestRewriteUserID_LegacyFormatPreservesSessionID(t *testing.T) {
	svc := NewIdentityService(&identityCacheStub{})

	const (
		clientDeviceID  = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
		clientSessionID = "00000000-1111-4222-8333-444444444444"
		cachedClientID  = "feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface"
		accountUUID     = "99999999-8888-4777-8666-555555555555"
	)
	// Legacy format: user_<deviceID>_account_<uuid?>_session_<sessionID>
	originalUserID := "user_" + clientDeviceID + "_account__session_" + clientSessionID
	body := []byte(`{"messages":[],"metadata":{"user_id":` + strconvQuote(originalUserID) + `}}`)

	result, err := svc.RewriteUserID(body, 555, accountUUID, cachedClientID, "claude-cli/2.1.50")
	require.NoError(t, err)

	rewrittenUserID := gjson.GetBytes(result, "metadata.user_id").String()
	require.NotEmpty(t, rewrittenUserID)

	parsed := ParseMetadataUserID(rewrittenUserID)
	require.NotNil(t, parsed)

	require.Equal(t, cachedClientID, parsed.DeviceID)
	require.Equal(t, accountUUID, parsed.AccountUUID)
	require.Equal(t, clientSessionID, parsed.SessionID, "session_id must be preserved across legacy format too")
}

func TestIdentityService_RewriteUserIDWithMasking_PreservesTopLevelFieldOrder(t *testing.T) {
	cache := &identityCacheStub{maskedSessionID: "11111111-2222-4333-8444-555555555555"}
	svc := NewIdentityService(cache)

	originalUserID := FormatMetadataUserID(
		"d61f76d0730d2b920763648949bad5c79742155c27037fc77ac3f9805cb90169",
		"",
		"7578cf37-aaca-46e4-a45c-71285d9dbb83",
		"2.1.78",
	)
	body := []byte(`{"alpha":1,"messages":[],"metadata":{"user_id":` + strconvQuote(originalUserID) + `},"max_tokens":64000,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"stream":true}`)

	account := &Account{
		ID:       123,
		Platform: PlatformAnthropic,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"session_id_masking_enabled": true,
		},
	}

	result, err := svc.RewriteUserIDWithMasking(context.Background(), body, account, "acc-uuid", "client-xyz", "claude-cli/2.1.78 (external, cli)")
	require.NoError(t, err)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"messages"`, `"metadata"`, `"max_tokens"`, `"thinking"`, `"output_config"`, `"stream"`)
	require.Contains(t, resultStr, cache.maskedSessionID)
	require.True(t, strings.Contains(resultStr, `"metadata":{"user_id":"`))
}

func strconvQuote(v string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}

// TestCreateFingerprintFromHeaders_NonClaudeCLIUserAgentFallsBackToDefault
// pins the regression: when the client User-Agent is not in claude-cli/X.Y.Z
// form (e.g. PostmanRuntime, okhttp, LobeChat, custom scripts), it must NOT
// be written into the account fingerprint cache, otherwise:
//   - ExtractCLIVersion(fp.UserAgent) returns "" → metadata.user_id falls back
//     to the legacy concatenated format, which is a third-party signal because
//     real claude-cli/2.1.78+ uses the JSON format.
//   - syncBillingHeaderVersion can't extract a version → cc_version sync becomes
//     a no-op even if a billing header block exists in the body.
func TestCreateFingerprintFromHeaders_NonClaudeCLIUserAgentFallsBackToDefault(t *testing.T) {
	svc := NewIdentityService(&identityCacheStub{})

	cases := []struct {
		name string
		ua   string
	}{
		{"empty", ""},
		{"postman", "PostmanRuntime/7.41.2"},
		{"okhttp", "okhttp/4.12.0"},
		{"browser", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)"},
		{"lobechat", "LobeChat/1.42.0"},
		{"curl", "curl/8.7.1"},
		{"app_ua_not_sdk_ua", "claude-code/2.1.100"}, // bootstrap UA, not SDK UA
		{"missing_version", "claude-cli/"},
		{"prefix_only", "claude-cli"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.ua != "" {
				h.Set("User-Agent", tc.ua)
			}
			fp := svc.createFingerprintFromHeaders(h)
			require.Equal(t, defaultFingerprint.UserAgent, fp.UserAgent,
				"non-claude-cli UA %q must fall back to default to avoid fingerprint pollution", tc.ua)
			// Version extraction must succeed on the resulting fingerprint UA.
			require.NotEmpty(t, ExtractCLIVersion(fp.UserAgent),
				"resulting fp.UserAgent must yield a non-empty CLI version")
		})
	}
}

// TestCreateFingerprintFromHeaders_ClaudeCLIUserAgentIsAdopted verifies that a
// genuine claude-cli/X.Y.Z User-Agent is still adopted as-is, so version-aware
// downstream logic (FormatMetadataUserID, syncBillingHeaderVersion) keeps
// working for real CC clients.
func TestCreateFingerprintFromHeaders_ClaudeCLIUserAgentIsAdopted(t *testing.T) {
	svc := NewIdentityService(&identityCacheStub{})

	cases := []struct {
		name        string
		ua          string
		wantVersion string
	}{
		{"plain", "claude-cli/2.1.100", "2.1.100"},
		{"with_suffix", "claude-cli/2.1.100 (external, cli)", "2.1.100"},
		{"older_version", "claude-cli/2.1.22 (external, cli)", "2.1.22"},
		{"prerelease", "claude-cli/2.1.100-beta", "2.1.100"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("User-Agent", tc.ua)
			fp := svc.createFingerprintFromHeaders(h)
			require.Equal(t, tc.ua, fp.UserAgent)
			require.Equal(t, tc.wantVersion, ExtractCLIVersion(fp.UserAgent))
		})
	}
}
