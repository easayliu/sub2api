package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// Quota probe mirrors the startup behaviour captured in real
// claude-cli/2.1.100 traffic (see capture/008):
//
//	POST /v1/messages?beta=true
//	{ "max_tokens":1, "messages":[{"content":"quota","role":"user"}],
//	  "model":"claude-haiku-4-5-20251001" }
//
// Anthropic's risk control (GMT 03:00 batch) looks for the presence of these
// supporting requests in each 5-hour reset window. A proxy-ed account that
// only ever emits arbitrary /v1/messages traffic without the periodic quota
// probe is easily distinguishable from a real CLI session. Firing one probe
// per 5h window is sufficient to fill that gap at near-zero cost
// (max_tokens=1 on haiku).

const (
	// quotaProbeModel is the haiku model the real CLI uses for its startup
	// quota probe. Keep this in sync with capture/008.
	quotaProbeModel = "claude-haiku-4-5-20251001"

	// quotaProbeTimeout is an upper bound for the probe request. The probe is
	// fire-and-forget from the caller's perspective so this only exists to
	// ensure the detached goroutine exits in bounded time on upstream hangs.
	quotaProbeTimeout = 20 * time.Second

	// quotaProbeMinInterval is the floor interval between probes when we have
	// no reliable 5h reset information (e.g. probe ran but response header
	// parsing failed). 4h30m stays safely under one reset window so a new
	// window always gets its own probe even in the absence of header signals.
	quotaProbeMinInterval = 4*time.Hour + 30*time.Minute

	// quotaProbeBodyJSON is the exact body sent by the real CLI quota
	// probe. Stored as a const string so each launch can cheaply materialise
	// a fresh []byte via `[]byte(quotaProbeBodyJSON)` without any risk of
	// concurrent goroutines mutating a shared slice. The pipeline will
	// inject metadata.user_id during buildUpstreamRequest via
	// RewriteUserIDWithMasking — we seed an empty metadata object so
	// rewriting is a no-op when the account has no fingerprint yet.
	quotaProbeBodyJSON = `{"max_tokens":1,"messages":[{"content":"quota","role":"user"}],"metadata":{"user_id":""},"model":"` + quotaProbeModel + `"}`
)

// quotaProbeState tracks per-account state for the quota probe scheduler.
// All fields are read/written via atomics so no lock is required on the
// hot path.
type quotaProbeState struct {
	// lastProbeUnix is the unix-second timestamp at which we most recently
	// fired a quota probe for this account. Zero means we have never fired.
	lastProbeUnix atomic.Int64

	// currentResetUnix is the most recent value of the
	// Anthropic-Ratelimit-Unified-5h-Reset response header we observed.
	// When this becomes greater than lastProbeUnix, a new 5h window has
	// started and we should fire a new probe on next use.
	currentResetUnix atomic.Int64
}

// quotaProbeStore maps accountID (int64) → *quotaProbeState.
var quotaProbeStore sync.Map

// quotaProbeCtxKeyType is the unexported context key used to mark an
// upstream request as the probe itself, preventing recursive probe
// launches and billing bookkeeping on the probe traffic.
type quotaProbeCtxKeyType struct{}

var quotaProbeCtxKey = quotaProbeCtxKeyType{}

// markQuotaProbeContext returns a context marked as belonging to a quota
// probe request.
func markQuotaProbeContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, quotaProbeCtxKey, true)
}

// isQuotaProbeContext reports whether ctx was marked by
// markQuotaProbeContext. Currently used to suppress re-entry into the
// probe scheduler from within the probe request itself.
func isQuotaProbeContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(quotaProbeCtxKey).(bool)
	return v
}

// getQuotaProbeState returns (and lazily creates) the tracking state for
// the given account ID. The sync.Map only ever stores *quotaProbeState
// values, so the type assertions are guaranteed to succeed; the
// comma-ok form is used to satisfy errcheck.
func getQuotaProbeState(accountID int64) *quotaProbeState {
	if v, ok := quotaProbeStore.Load(accountID); ok {
		if st, ok := v.(*quotaProbeState); ok {
			return st
		}
	}
	st := &quotaProbeState{}
	actual, _ := quotaProbeStore.LoadOrStore(accountID, st)
	if existing, ok := actual.(*quotaProbeState); ok {
		return existing
	}
	return st
}

// claimQuotaProbeSlot atomically decides whether the caller should fire a
// quota probe for this account right now. It returns true exactly once
// per "probe opportunity" (first-use, reset-window-transition, or safety
// floor), and false otherwise. Concurrent callers all see false except
// the one that wins the CAS.
func claimQuotaProbeSlot(accountID int64) bool {
	st := getQuotaProbeState(accountID)
	now := time.Now().Unix()
	last := st.lastProbeUnix.Load()

	// Condition A: first ever use of this account in this process — fire
	// a probe immediately so the 5h window gets its startup request.
	if last == 0 {
		return st.lastProbeUnix.CompareAndSwap(0, now)
	}

	// Condition B: response-header tracker noticed a new reset window that
	// started after our last probe. This is the accurate signal.
	if reset := st.currentResetUnix.Load(); reset > 0 && reset > last {
		return st.lastProbeUnix.CompareAndSwap(last, now)
	}

	// Condition C: safety floor — we have not been able to read reset
	// headers (upstream errors / non-anthropic responses) but enough real
	// time has elapsed that we are almost certainly in a new window.
	if now-last >= int64(quotaProbeMinInterval.Seconds()) {
		return st.lastProbeUnix.CompareAndSwap(last, now)
	}

	return false
}

// updateQuotaProbeResetFromResponse extracts the
// Anthropic-Ratelimit-Unified-5h-Reset value from an upstream response and
// records it against the account, so the next probe decision has accurate
// window information. Safe to call with nil / missing headers.
func updateQuotaProbeResetFromResponse(accountID int64, respHeader http.Header) {
	if respHeader == nil {
		return
	}
	raw := strings.TrimSpace(respHeader.Get("Anthropic-Ratelimit-Unified-5h-Reset"))
	if raw == "" {
		return
	}
	ts, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ts <= 0 {
		return
	}
	st := getQuotaProbeState(accountID)
	for {
		cur := st.currentResetUnix.Load()
		if ts <= cur {
			return
		}
		if st.currentResetUnix.CompareAndSwap(cur, ts) {
			return
		}
	}
}

// launchQuotaProbe fires a quota probe request for the given OAuth account
// in a detached goroutine. Returns immediately; the caller does not wait
// and failures are logged without affecting the real user request.
//
// The probe reuses the full buildUpstreamRequest pipeline (fingerprint /
// billing header sync / mimic headers / wire casing) so it is
// indistinguishable from the real CLI's startup quota probe. The probe
// request's context is marked via markQuotaProbeContext to prevent
// re-entrant probe launches when buildUpstreamRequest calls back into
// this scheduler.
func (s *GatewayService) launchQuotaProbe(
	account *Account,
	token string,
	tokenType string,
	proxyURL string,
	tlsProfile *tlsfingerprint.Profile,
	mimicClaudeCode bool,
) {
	if account == nil || s == nil || s.httpUpstream == nil {
		return
	}

	// Snapshot everything we need out of the caller frame; the goroutine
	// must not touch *gin.Context because that is bound to the caller's
	// request lifetime.
	accountID := account.ID
	concurrency := account.Concurrency

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), quotaProbeTimeout)
		defer cancel()
		ctx = markQuotaProbeContext(ctx)

		// Fresh []byte from the const string. Downstream mutations
		// (RewriteUserIDWithMasking, sjson rewrites) may reallocate this
		// slice, but cannot affect any other goroutine.
		body := []byte(quotaProbeBodyJSON)

		// Pass c=nil. The real forwarding path uses *gin.Context for
		// request-scoped metadata; the probe has none of that and
		// buildUpstreamRequest gracefully falls back when c is nil.
		req, err := s.buildUpstreamRequest(ctx, nil, account, body, token, tokenType, quotaProbeModel, false, mimicClaudeCode)
		if err != nil {
			slog.Warn("quota probe: build upstream request failed",
				"account_id", accountID, "error", err)
			return
		}

		resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, accountID, concurrency, tlsProfile)
		if err != nil {
			slog.Warn("quota probe: upstream send failed",
				"account_id", accountID, "error", err)
			return
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()

		updateQuotaProbeResetFromResponse(accountID, resp.Header)

		slog.Info("quota probe: sent",
			"account_id", accountID,
			"status", resp.StatusCode,
			"mimic", mimicClaudeCode,
		)
	}()
}
