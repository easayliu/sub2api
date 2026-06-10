//go:build unit

package repository

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFingerprintKey(t *testing.T) {
	tests := []struct {
		name      string
		accountID int64
		platform  string
		expected  string
	}{
		{
			name:      "normal_account_id",
			accountID: 123,
			platform:  "MacOS",
			expected:  "fingerprint:123:MacOS",
		},
		{
			name:      "windows_platform",
			accountID: 123,
			platform:  "Windows",
			expected:  "fingerprint:123:Windows",
		},
		{
			name:      "zero_account_id",
			accountID: 0,
			platform:  "Linux",
			expected:  "fingerprint:0:Linux",
		},
		{
			name:      "negative_account_id",
			accountID: -1,
			platform:  "MacOS",
			expected:  "fingerprint:-1:MacOS",
		},
		{
			name:      "max_int64",
			accountID: math.MaxInt64,
			platform:  "MacOS",
			expected:  "fingerprint:9223372036854775807:MacOS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fingerprintKey(tc.accountID, tc.platform)
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestLegacyFingerprintKey(t *testing.T) {
	tests := []struct {
		name      string
		accountID int64
		expected  string
	}{
		{
			name:      "normal_account_id",
			accountID: 123,
			expected:  "fingerprint:123",
		},
		{
			name:      "zero_account_id",
			accountID: 0,
			expected:  "fingerprint:0",
		},
		{
			name:      "max_int64",
			accountID: math.MaxInt64,
			expected:  "fingerprint:9223372036854775807",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := legacyFingerprintKey(tc.accountID)
			require.Equal(t, tc.expected, got)
		})
	}
}

func TestMaskedSessionKey(t *testing.T) {
	require.Equal(t, "masked_session:123:MacOS", maskedSessionKey(123, "MacOS"))
	require.Equal(t, "masked_session:123:Windows", maskedSessionKey(123, "Windows"))
	require.Equal(t, "masked_session:123", legacyMaskedSessionKey(123))
}
