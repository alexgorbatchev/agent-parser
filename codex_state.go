package parser

// The helpers below are nil-safe so the stateless ParseCodexLine path can share
// every record handler with the stateful CodexParser.

func (p *CodexParser) setModel(model string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.currentModel = model
	p.mu.Unlock()
}

// usageContext returns the active model and the context window to report,
// remembering a newly reported window for later records.
func (p *CodexParser) usageContext(reportedWindow int64) (string, int64) {
	if p == nil {
		return "", reportedWindow
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if reportedWindow == 0 {
		reportedWindow = p.contextWindow
	}
	if reportedWindow > 0 {
		p.contextWindow = reportedWindow
	}
	return p.currentModel, reportedWindow
}

// recordTokenCountRateLimits updates the cached rate-limit reset from a
// token_count record.
func (p *CodexParser) recordTokenCountRateLimits(resetAt, retrySec *int64, rateLimits *rawCodexRateLimits, ts int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case resetAt != nil:
		p.rateLimitResetAt = resetAt
		p.retryAfterSeconds = retrySec
	case codexQuotaAvailable(rateLimits):
		p.clearRateLimitLocked()
	default:
		p.expireRateLimitLocked(ts)
	}
}

// recordErrorRateLimits updates the cached rate-limit reset from an error
// record's own rate-limit payload, then expires a reset that has passed.
func (p *CodexParser) recordErrorRateLimits(hasRateLimits bool, resetAt, retrySec *int64, ts int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if hasRateLimits && resetAt != nil {
		p.rateLimitResetAt = resetAt
		p.retryAfterSeconds = retrySec
	}
	p.expireRateLimitLocked(ts)
}

// pendingRateLimitReset returns the cached reset and the whole seconds left
// until it, when that reset is still in the future.
func (p *CodexParser) pendingRateLimitReset(ts int64) (*int64, *int64) {
	if p == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rateLimitResetAt == nil || *p.rateLimitResetAt <= ts {
		return nil, nil
	}
	remSec := (*p.rateLimitResetAt - ts + 999) / 1000
	if remSec <= 0 {
		return p.rateLimitResetAt, nil
	}
	return p.rateLimitResetAt, &remSec
}

func (p *CodexParser) expireRateLimitLocked(ts int64) {
	if p.rateLimitResetAt != nil && *p.rateLimitResetAt <= ts {
		p.clearRateLimitLocked()
	}
}

func (p *CodexParser) clearRateLimitLocked() {
	p.rateLimitResetAt = nil
	p.retryAfterSeconds = nil
}

// isRepeatedUsage records a token_count's cumulative usage and reports whether
// it equals the previous record's, which means the record reports no new model
// response. The stateless path cannot tell and reports false.
func (p *CodexParser) isRepeatedUsage(total rawCodexTokenBreakdown) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	repeated := p.lastTotalUsage != nil && *p.lastTotalUsage == total
	p.lastTotalUsage = &total
	return repeated
}
