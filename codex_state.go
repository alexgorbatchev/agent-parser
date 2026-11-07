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
func (p *CodexParser) recordErrorRateLimits(resetAt, retrySec *int64, ts int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if resetAt != nil {
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

// recordHistoryStart keeps the subagent history start and history mode of the
// rollout's own session_meta. Later session_meta records belong to inherited
// parent history.
func (p *CodexParser) recordHistoryStart(start *uint64, paginated bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessionMetaSeen {
		return
	}
	p.sessionMetaSeen = true
	p.historyStartOrdinal = start
	p.paginated = paginated
}

// isInheritedHistory reports whether a record of a forked subagent's rollout
// replays the parent's history: Codex keeps records below the fork's
// subagent_history_start_ordinal out of the child's own turns and items. The
// stateless path has no session_meta to read and reports false.
func (p *CodexParser) isInheritedHistory(ordinal *uint64) bool {
	if p == nil || ordinal == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.historyStartOrdinal != nil && *ordinal < *p.historyStartOrdinal
}

// beginTurn opens a turn at task_started. The stateful parser holds its
// turn_start until the turn's first prompt or other event; the stateless path
// reports it at once. A turn that was still open is reported if it never was.
func (p *CodexParser) beginTurn(start ParsedEvent) []ParsedEvent {
	if p == nil {
		return []ParsedEvent{start}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var events []ParsedEvent
	if p.pendingTurnStart != nil {
		events = append(events, *p.pendingTurnStart)
	}
	p.turnOpen = true
	p.pendingTurnStart = &start
	return events
}

// promptEvents places a user prompt in the open turn. The turn's first prompt
// is reported on its turn_start; a later prompt in the same turn is input
// absorbed into it and is reported as message_absorbed. A prompt outside a
// task turn, as in rollouts without task events, starts a turn itself.
func (p *CodexParser) promptEvents(prompt []ParsedEvent) []ParsedEvent {
	if p == nil || len(prompt) == 0 {
		return prompt
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	data := prompt[0].Data
	switch {
	case p.pendingTurnStart != nil:
		start := *p.pendingTurnStart
		p.pendingTurnStart = nil
		start.Data.Role = data.Role
		start.Data.Content = data.Content
		start.Data.LastPrompt = data.LastPrompt
		start.Data.Title = data.Title
		start.Data.Images = data.Images
		return []ParsedEvent{start}
	case p.turnOpen:
		return []ParsedEvent{{
			EventType: "message_absorbed",
			Timestamp: prompt[0].Timestamp,
			Data: EventData{
				Role:    data.Role,
				Content: data.Content,
				Images:  data.Images,
			},
		}}
	}
	return prompt
}

// pendingTurn returns the turn_start the open turn is holding, if any.
func (p *CodexParser) pendingTurn() *ParsedEvent {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pendingTurnStart
}

// takePendingTurnStart returns the held turn_start if it is still the one
// held before the current record, so a turn opened by this record is not
// reported before it begins.
func (p *CodexParser) takePendingTurnStart(before *ParsedEvent) *ParsedEvent {
	if p == nil || before == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pendingTurnStart != before {
		return nil
	}
	p.pendingTurnStart = nil
	return before
}

// endTurn closes the open turn and returns its turn_start if no event has
// reported it yet.
func (p *CodexParser) endTurn() []ParsedEvent {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turnOpen = false
	start := p.pendingTurnStart
	p.pendingTurnStart = nil
	if start == nil {
		return nil
	}
	return []ParsedEvent{*start}
}

// isPaginated reports whether the rollout declared paginated history. The
// stateless path cannot know and reports false.
func (p *CodexParser) isPaginated() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paginated
}

func (p *CodexParser) advanceLine() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.line++
	p.mu.Unlock()
}

// recordUserImages keeps a user response_item's images for the UserMessage
// item on the next line.
func (p *CodexParser) recordUserImages(images []Image) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.userImages = images
	p.userImagesLine = p.line
}

// takeAdjacentUserImages returns the images of the user response_item on the
// previous line, if that is where they were recorded.
func (p *CodexParser) takeAdjacentUserImages() ([]Image, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.userImagesLine == 0 || p.userImagesLine != p.line-1 {
		return nil, false
	}
	images := p.userImages
	p.userImages = nil
	p.userImagesLine = 0
	return images, true
}
