package parser

import "math"

// These fields project Codex's RateLimitSnapshot onto the window data used by
// normalized events. Account metadata does not participate in reset selection.
type rawCodexRateLimits struct {
	Primary              *rawCodexRateLimitWindow `json:"primary"`
	Secondary            *rawCodexRateLimitWindow `json:"secondary"`
	RateLimitReachedType *string                  `json:"rate_limit_reached_type"`
}

type rawCodexRateLimitWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes *int64  `json:"window_minutes"`
	ResetsAt      *int64  `json:"resets_at"`
}

func selectCodexRateLimitReset(limits *rawCodexRateLimits, nowMs int64) (*int64, *int64) {
	if limits == nil {
		return nil, nil
	}
	var resetAt int64
	for _, window := range []*rawCodexRateLimitWindow{limits.Primary, limits.Secondary} {
		if window == nil || (window.UsedPercent < 100 && limits.RateLimitReachedType == nil) || window.ResetsAt == nil {
			continue
		}
		seconds := *window.ResetsAt
		if seconds <= 0 || seconds > math.MaxInt64/1000 {
			continue
		}
		// Both exhausted windows must have recovered before retrying.
		resetAt = max(resetAt, seconds*1000)
	}
	if resetAt <= nowMs {
		return nil, nil
	}
	remaining := resetAt - nowMs
	retry := remaining / 1000
	if remaining%1000 != 0 {
		retry++
	}
	return &resetAt, &retry
}

func codexQuotaAvailable(limits *rawCodexRateLimits) bool {
	if limits == nil || limits.RateLimitReachedType != nil {
		return false
	}
	available := false
	for _, window := range []*rawCodexRateLimitWindow{limits.Primary, limits.Secondary} {
		if window != nil {
			if window.UsedPercent >= 100 {
				return false
			}
			available = true
		}
	}
	return available
}
