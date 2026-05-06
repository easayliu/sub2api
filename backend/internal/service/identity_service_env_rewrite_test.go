package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newEnvRewriteService() *IdentityService {
	return NewIdentityService(&identityCacheStub{})
}

// Body shape mirrors what Claude Code sends to /v1/messages: system is an
// array of typed text blocks, and the env preamble lives inside one of them.
func buildEnvBody(t *testing.T, envText string) []byte {
	t.Helper()
	payload := map[string]any{
		"model": "claude-sonnet-4-5",
		"system": []map[string]any{
			{"type": "text", "text": "You are Claude Code..."},
			{"type": "text", "text": envText},
		},
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	return b
}

func TestRewriteEnvSection_NormalizesDarwinProfile(t *testing.T) {
	svc := newEnvRewriteService()
	envText := strings.Join([]string{
		"# Env",
		"You have been invoked in the following environment:",
		" - Primary working directory: /tmp/x",
		"  - Platform: linux",
		"  - Shell: bash",
		"  - OS Version: Linux 6.8.0-generic",
	}, "\n")

	body := buildEnvBody(t, envText)
	out := svc.RewriteEnvSection(body, &defaultFingerprint)

	got := gjson.GetBytes(out, "system.1.text").String()
	require.Contains(t, got, "  - Platform: darwin")
	require.Contains(t, got, "  - Shell: zsh")
	require.Contains(t, got, "  - OS Version: Darwin 25.3.0")
	require.NotContains(t, got, "Platform: linux")
	require.NotContains(t, got, "Shell: bash")
}

func TestRewriteEnvSection_NoSentinelUntouched(t *testing.T) {
	svc := newEnvRewriteService()
	// No env block sentinel — rewriter must not touch the text even if
	// Platform/OS lines exist (guards against accidental matches in user
	// content or tool descriptions).
	envText := strings.Join([]string{
		"Doc snippet:",
		"  - Platform: windows",
		"  - Shell: pwsh",
	}, "\n")

	body := buildEnvBody(t, envText)
	out := svc.RewriteEnvSection(body, &defaultFingerprint)

	got := gjson.GetBytes(out, "system.1.text").String()
	require.Equal(t, envText, got)
}

func TestRewriteEnvSection_AlreadyDarwinIsNoOp(t *testing.T) {
	svc := newEnvRewriteService()
	envText := strings.Join([]string{
		"You have been invoked in the following environment:",
		"  - Platform: darwin",
		"  - Shell: zsh",
		"  - OS Version: Darwin 25.3.0",
	}, "\n")

	body := buildEnvBody(t, envText)
	out := svc.RewriteEnvSection(body, &defaultFingerprint)

	got := gjson.GetBytes(out, "system.1.text").String()
	require.Equal(t, envText, got)
}

func TestRewriteEnvSection_StringSystemLeftAlone(t *testing.T) {
	svc := newEnvRewriteService()
	// Legacy wire format where system is a plain string. Current rewriter
	// only handles the array form; assert it's a safe no-op for strings so
	// non-Claude-Code clients aren't broken.
	payload := map[string]any{
		"model":  "claude-sonnet-4-5",
		"system": "You have been invoked in the following environment:\n  - Platform: linux",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	out := svc.RewriteEnvSection(body, &defaultFingerprint)
	require.JSONEq(t, string(body), string(out))
}

// Real sample captured from a live Claude Code CLI session. Note the
// 1-space (not 2-space) indent before `- Platform:` — the regex must
// tolerate either.
func TestRewriteEnvSection_RealClaudeCodeSample(t *testing.T) {
	svc := newEnvRewriteService()
	envText := `# Environment
You have been invoked in the following environment:
 - Primary working directory: /Users/mac
  - Is a git repository: false
 - Platform: darwin
 - Shell: zsh
 - OS Version: Darwin 25.4.0
`

	body := buildEnvBody(t, envText)
	out := svc.RewriteEnvSection(body, &defaultFingerprint)
	got := gjson.GetBytes(out, "system.1.text").String()

	require.Contains(t, got, " - Platform: darwin")
	require.Contains(t, got, " - Shell: zsh")
	require.Contains(t, got, " - OS Version: Darwin 25.3.0")
	require.NotContains(t, got, "Darwin 25.4.0")
	// Sibling lines must be untouched.
	require.Contains(t, got, " - Primary working directory: /Users/mac")
	require.Contains(t, got, "  - Is a git repository: false")
}

// Regression guard for the earlier `\s+` bug: a Platform line with an empty
// value must not cause the regex to span into the following Shell line.
func TestRewriteEnvSection_EmptyPlatformValueDoesNotSpanLines(t *testing.T) {
	svc := newEnvRewriteService()
	envText := strings.Join([]string{
		"You have been invoked in the following environment:",
		"  - Platform: ",
		"  - Shell: bash",
		"  - OS Version: Linux 6.8",
	}, "\n")

	body := buildEnvBody(t, envText)
	out := svc.RewriteEnvSection(body, &defaultFingerprint)

	got := gjson.GetBytes(out, "system.1.text").String()
	// Every key must still appear as its own line with the correct value.
	require.Contains(t, got, "  - Platform: darwin")
	require.Contains(t, got, "  - Shell: zsh")
	require.Contains(t, got, "  - OS Version: Darwin 25.3.0")
	// The Shell line must not have been swallowed by the Platform match.
	require.Equal(t, 3, strings.Count(got, "\n  - "))
}

// Real Windows-client capture (capture/0505/claude-validator.json): the env
// preamble leaks `C:\Users\easayliu` and a PowerShell shell description while
// claiming Platform: win32. After rewrite, every OS-coupled line — including
// the working dir — must look like a Mac client.
func TestRewriteEnvSection_WindowsClientFullyNormalized(t *testing.T) {
	svc := newEnvRewriteService()
	envText := strings.Join([]string{
		"You have been invoked in the following environment: ",
		" - Primary working directory: C:\\Users\\easayliu",
		" - Is a git repository: false",
		" - Platform: win32",
		" - Shell: PowerShell (use PowerShell syntax — e.g., $null not /dev/null, $env:VAR not $VAR, backtick for line continuation)",
		" - OS Version: Windows 11 Enterprise 10.0.26200",
	}, "\n")

	body := buildEnvBody(t, envText)
	out := svc.RewriteEnvSection(body, &defaultFingerprint)

	got := gjson.GetBytes(out, "system.1.text").String()
	require.Contains(t, got, " - Primary working directory: /Users/easayliu")
	require.Contains(t, got, " - Platform: darwin")
	require.Contains(t, got, " - Shell: zsh")
	require.Contains(t, got, " - OS Version: Darwin 25.3.0")
	// Windows-specific markers must be gone.
	require.NotContains(t, got, "C:\\Users")
	require.NotContains(t, got, "win32")
	require.NotContains(t, got, "PowerShell")
	require.NotContains(t, got, "Windows 11")
}

func TestNormalizeWorkingDirToMac(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"windows users home", `C:\Users\easayliu`, "/Users/easayliu"},
		{"windows users nested", `C:\Users\easayliu\Works\proj`, "/Users/easayliu/Works/proj"},
		{"windows users forward slash", `C:/Users/foo/bar`, "/Users/foo/bar"},
		{"windows non-users path", `C:\Workspace\proj`, "/Users/user/Workspace/proj"},
		{"windows drive root", `D:\`, "/Users/user"},
		{"windows lowercase drive", `d:\Users\foo`, "/Users/foo"},
		{"linux home", "/home/ubuntu/work/proj", "/Users/ubuntu/work/proj"},
		{"linux home root", "/home/root", "/Users/root"},
		{"mac path untouched", "/Users/mac/foo/bar", "/Users/mac/foo/bar"},
		{"unix non-home untouched", "/var/lib/foo", "/var/lib/foo"},
		{"unc path", `\\server\share\proj`, "/Users/user/server/share/proj"},
		{"empty value", "", ""},
		{"trailing slash trimmed", `C:\Users\foo\`, "/Users/foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeWorkingDirToMac(tc.in)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRewriteEnvSection_MacWorkingDirIsNoOp(t *testing.T) {
	// Real Mac CLI traffic: every value already matches the Mac profile, so
	// the rewriter must not perturb the body at all (idempotency guard).
	svc := newEnvRewriteService()
	envText := strings.Join([]string{
		"You have been invoked in the following environment:",
		" - Primary working directory: /Users/easayliu/Works/easay/sub2api",
		" - Platform: darwin",
		" - Shell: zsh",
		" - OS Version: Darwin 25.3.0",
	}, "\n")

	body := buildEnvBody(t, envText)
	out := svc.RewriteEnvSection(body, &defaultFingerprint)

	got := gjson.GetBytes(out, "system.1.text").String()
	require.Equal(t, envText, got)
}

func TestApplyLockedProfile_MigratesLegacyFingerprint(t *testing.T) {
	// Legacy un-bucketed fingerprints were locked to Mac. After bucketing, the
	// MacOS bucket migration should still re-pin OS/Arch/Prompt* to the Mac
	// profile when they drift.
	fp := &Fingerprint{
		StainlessOS:   "Linux",
		StainlessArch: "x64",
	}
	changed := applyLockedProfile(fp, PlatformMacOS)
	require.True(t, changed)
	require.Equal(t, "MacOS", fp.StainlessOS)
	require.Equal(t, "arm64", fp.StainlessArch)
	require.Equal(t, "darwin", fp.PromptPlatform)
	require.Equal(t, "Darwin 25.3.0", fp.PromptOSVersion)
	require.Equal(t, "zsh", fp.PromptShell)

	// Second call must be a no-op once fingerprint already matches profile.
	require.False(t, applyLockedProfile(fp, PlatformMacOS))
}

// Platform bucketing means a Windows-bucket fingerprint is its own canonical
// shape — applyLockedProfile must align to win32 / Windows / x64 instead of
// forcing Mac.
func TestApplyLockedProfile_WindowsBucketLocksToWindowsProfile(t *testing.T) {
	fp := &Fingerprint{}
	require.True(t, applyLockedProfile(fp, PlatformWindows))
	require.Equal(t, "Windows", fp.StainlessOS)
	require.Equal(t, "x64", fp.StainlessArch)
	require.Equal(t, "win32", fp.PromptPlatform)
	require.Equal(t, "Windows 11 Enterprise 10.0.26200", fp.PromptOSVersion)
	require.Contains(t, fp.PromptShell, "PowerShell")
	require.False(t, applyLockedProfile(fp, PlatformWindows))
}

// All accounts pin to the Mac platform; no detectPlatform / Windows-bucket
// passthrough behavior exists anymore. Tests for those removed paths used
// to live here.
