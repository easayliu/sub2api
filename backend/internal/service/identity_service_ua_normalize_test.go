package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeCLIUA(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "canonical (external, cli) is unchanged",
			in:   "claude-cli/2.1.131 (external, cli)",
			want: "claude-cli/2.1.131 (external, cli)",
		},
		{
			name: "(external, sdk-cli) suffix is rewritten",
			in:   "claude-cli/2.1.109 (external, sdk-cli)",
			want: "claude-cli/2.1.109 (external, cli)",
		},
		{
			name: "(external, claude-vscode, agent-sdk/X.Y.Z) suffix is rewritten",
			in:   "claude-cli/2.1.126 (external, claude-vscode, agent-sdk/0.2.126)",
			want: "claude-cli/2.1.126 (external, cli)",
		},
		{
			name: "claude-cli without suffix gets canonical suffix appended",
			in:   "claude-cli/2.1.123",
			want: "claude-cli/2.1.123 (external, cli)",
		},
		{
			name: "uppercase prefix matches and normalizes",
			in:   "Claude-CLI/2.1.131 (External, SDK-CLI)",
			want: "claude-cli/2.1.131 (external, cli)",
		},
		{
			name: "non-claude-cli UA passes through unchanged",
			in:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
			want: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
		},
		{
			name: "empty string passes through",
			in:   "",
			want: "",
		},
		{
			name: "claude-cli with malformed version is left alone",
			in:   "claude-cli/abc (external, cli)",
			want: "claude-cli/abc (external, cli)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeCLIUA(tt.in))
		})
	}
}

func TestGetOrCreateFingerprint_NormalizesUASuffixOnFirstAndUpgrade(t *testing.T) {
	cache := &identityCacheStub{}
	svc := NewIdentityService(cache)

	// First request: sdk-cli family — gets normalized at create time.
	first := http.Header{}
	first.Set("User-Agent", "claude-cli/2.1.109 (external, sdk-cli)")
	fp1, err := svc.GetOrCreateFingerprint(context.Background(), 42, first)
	require.NoError(t, err)
	require.Equal(t, "claude-cli/2.1.109 (external, cli)", fp1.UserAgent,
		"sdk-cli suffix must be normalised to (external, cli) on initial create")

	// Second request: vscode family with newer version — must upgrade version
	// AND keep the canonical (external, cli) suffix in cache.
	cache2 := &identityCacheStub{}
	svc2 := NewIdentityService(cache2)
	stored := &Fingerprint{
		ClientID:                "abc",
		UserAgent:               "claude-cli/2.1.109 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.81.0",
		StainlessOS:             "MacOS",
		StainlessArch:           "arm64",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v24.3.0",
		PromptPlatform:          "darwin",
		PromptOSVersion:         "Darwin 25.3.0",
		PromptShell:             "zsh",
	}
	require.NoError(t, cache2.SetFingerprint(context.Background(), 99, "MacOS", stored))
	cache2WithSeed := &cacheStubWithGet{seeded: stored}
	svc2 = NewIdentityService(cache2WithSeed)

	upgrade := http.Header{}
	upgrade.Set("User-Agent", "claude-cli/2.1.131 (external, claude-vscode, agent-sdk/0.2.126)")
	fp2, err := svc2.GetOrCreateFingerprint(context.Background(), 99, upgrade)
	require.NoError(t, err)
	require.Equal(t, "claude-cli/2.1.131 (external, cli)", fp2.UserAgent,
		"upgrade path must rewrite vscode/agent-sdk suffix to canonical")
	require.Equal(t, "abc", fp2.ClientID, "ClientID must remain stable across UA family change")
}

// cacheStubWithGet returns a seeded fingerprint on the first GetFingerprint
// call, then nil. Used to simulate "upgrade hit" without a full cache impl.
type cacheStubWithGet struct {
	seeded *Fingerprint
	got    bool
	stored *Fingerprint
}

func (s *cacheStubWithGet) GetFingerprint(_ context.Context, _ int64, _ string) (*Fingerprint, error) {
	if !s.got {
		s.got = true
		return s.seeded, nil
	}
	return s.stored, nil
}
func (s *cacheStubWithGet) SetFingerprint(_ context.Context, _ int64, _ string, fp *Fingerprint) error {
	s.stored = fp
	return nil
}
func (s *cacheStubWithGet) GetMaskedSessionID(_ context.Context, _ int64, _ string) (string, error) {
	return "", nil
}
func (s *cacheStubWithGet) SetMaskedSessionID(_ context.Context, _ int64, _, _ string) error {
	return nil
}
