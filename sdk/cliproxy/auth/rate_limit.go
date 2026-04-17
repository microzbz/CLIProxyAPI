package auth

import (
	"context"
	"strings"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

const (
	defaultAuthRateLimitWindow   = time.Minute
	defaultAuthRateLimitCooldown = time.Minute
)

type resolvedAuthRateLimit struct {
	limit    int
	window   time.Duration
	cooldown time.Duration
}

func authRateLimitConfigChanged(oldCfg, newCfg *internalconfig.Config) bool {
	if oldCfg == nil && newCfg == nil {
		return false
	}
	if oldCfg == nil || newCfg == nil {
		return true
	}
	oldRate := oldCfg.AuthRateLimit
	newRate := newCfg.AuthRateLimit
	if oldRate.Limit != newRate.Limit || oldRate.WindowSeconds != newRate.WindowSeconds || oldRate.CooldownSeconds != newRate.CooldownSeconds {
		return true
	}
	if len(oldRate.PerAuthRules) != len(newRate.PerAuthRules) {
		return true
	}
	for i := range oldRate.PerAuthRules {
		if strings.TrimSpace(oldRate.PerAuthRules[i]) != strings.TrimSpace(newRate.PerAuthRules[i]) {
			return true
		}
	}
	return false
}

func localRateLimitBlock(auth *Auth, now time.Time) (bool, time.Time) {
	if auth == nil {
		return false, time.Time{}
	}
	next := auth.LocalRateLimit.CooldownUntil
	if next.IsZero() || !next.After(now) {
		return false, time.Time{}
	}
	return true, next
}

func normalizeAuthRateLimitDuration(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func resolveAuthRateLimit(cfg *internalconfig.Config, auth *Auth) (resolvedAuthRateLimit, bool) {
	if auth == nil || cfg == nil {
		return resolvedAuthRateLimit{}, false
	}

	limit := cfg.AuthRateLimit.Limit
	for _, rawRule := range cfg.AuthRateLimit.PerAuthRules {
		identifier, field, value, ok := parseAuthRateLimitRule(rawRule)
		if !ok || !authRateLimitRuleMatches(auth, identifier) {
			continue
		}
		if field == "limit" {
			limit = value
		}
	}
	if override, ok := auth.AuthRateLimitLimitOverride(); ok {
		limit = override
	}
	if limit <= 0 {
		return resolvedAuthRateLimit{}, false
	}

	return resolvedAuthRateLimit{
		limit:    limit,
		window:   normalizeAuthRateLimitDuration(cfg.AuthRateLimit.WindowSeconds, defaultAuthRateLimitWindow),
		cooldown: normalizeAuthRateLimitDuration(cfg.AuthRateLimit.CooldownSeconds, defaultAuthRateLimitCooldown),
	}, true
}

func parseAuthRateLimitRule(raw string) (identifier string, field string, value int, ok bool) {
	parts := strings.Split(raw, "|")
	if len(parts) != 3 {
		log.WithField("rule", raw).Warn("ignoring invalid auth-rate-limit rule")
		return "", "", 0, false
	}
	identifier = strings.ToLower(strings.TrimSpace(parts[0]))
	field = strings.ToLower(strings.TrimSpace(parts[1]))
	if identifier == "" || field == "" {
		log.WithField("rule", raw).Warn("ignoring invalid auth-rate-limit rule")
		return "", "", 0, false
	}
	if field != "limit" {
		log.WithField("rule", raw).Warn("ignoring unsupported auth-rate-limit rule")
		return "", "", 0, false
	}
	parsed, okParse := parseIntAny(strings.TrimSpace(parts[2]))
	if !okParse {
		log.WithField("rule", raw).Warn("ignoring auth-rate-limit rule with invalid value")
		return "", "", 0, false
	}
	if parsed < 0 {
		parsed = 0
	}
	return identifier, field, parsed, true
}

func authRateLimitRuleMatches(auth *Auth, identifier string) bool {
	if auth == nil || identifier == "" {
		return false
	}
	for _, candidate := range authRateLimitIdentifiers(auth) {
		if candidate == identifier {
			return true
		}
	}
	return false
}

func authRateLimitIdentifiers(auth *Auth) []string {
	if auth == nil {
		return nil
	}
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	add := func(raw string) {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	add(auth.ID)
	add(auth.FileName)
	add(auth.Label)
	if auth.Attributes != nil {
		add(auth.Attributes["path"])
		add(auth.Attributes["source"])
	}
	if auth.Metadata != nil {
		if email, ok := auth.Metadata["email"].(string); ok {
			add(email)
		}
	}
	if _, account := auth.AccountInfo(); account != "" {
		add(account)
	}
	return out
}

func (m *Manager) resetAllAuthRateLimitState() {
	if m == nil {
		return
	}
	snapshots := make([]*Auth, 0)
	m.mu.Lock()
	for _, auth := range m.auths {
		if auth == nil {
			continue
		}
		if auth.LocalRateLimit.CooldownUntil.IsZero() && len(auth.LocalRateLimit.RequestTimestamps) == 0 {
			continue
		}
		auth.LocalRateLimit = LocalRateLimitState{}
		snapshots = append(snapshots, auth.Clone())
	}
	m.mu.Unlock()
	m.syncScheduler()
	for _, snapshot := range snapshots {
		m.persistAuthRateLimitState(snapshot)
	}
}

func (m *Manager) reserveAuthRateLimit(authID string) (bool, time.Time) {
	if m == nil {
		return true, time.Time{}
	}
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return true, time.Time{}
	}

	cfg, _ := m.runtimeConfig.Load().(*internalconfig.Config)
	now := time.Now()
	var snapshot *Auth
	persistState := false

	m.mu.Lock()
	auth := m.auths[authID]
	if auth == nil {
		m.mu.Unlock()
		return true, time.Time{}
	}

	limitCfg, enabled := resolveAuthRateLimit(cfg, auth)
	if !enabled {
		if auth.LocalRateLimit.CooldownUntil.IsZero() && len(auth.LocalRateLimit.RequestTimestamps) == 0 {
			m.mu.Unlock()
			return true, time.Time{}
		}
		auth.LocalRateLimit = LocalRateLimitState{}
		snapshot = auth.Clone()
		persistState = true
		m.mu.Unlock()
		m.finalizeAuthRateLimitStateChange(snapshot, persistState)
		return true, time.Time{}
	}

	state := &auth.LocalRateLimit
	if state.CooldownUntil.After(now) {
		retryAt := state.CooldownUntil
		m.mu.Unlock()
		return false, retryAt
	}
	if !state.CooldownUntil.IsZero() {
		state.CooldownUntil = time.Time{}
		snapshot = auth.Clone()
		persistState = true
	}

	if len(state.RequestTimestamps) > 0 {
		kept := state.RequestTimestamps[:0]
		cutoff := now.Add(-limitCfg.window)
		for _, ts := range state.RequestTimestamps {
			if ts.After(cutoff) {
				kept = append(kept, ts)
			}
		}
		state.RequestTimestamps = kept
	}

	if len(state.RequestTimestamps) >= limitCfg.limit {
		state.RequestTimestamps = nil
		state.CooldownUntil = now.Add(limitCfg.cooldown)
		snapshot = auth.Clone()
		persistState = true
		retryAt := state.CooldownUntil
		m.mu.Unlock()
		m.finalizeAuthRateLimitStateChange(snapshot, persistState)
		return false, retryAt
	}

	state.RequestTimestamps = append(state.RequestTimestamps, now)
	m.mu.Unlock()
	if snapshot != nil && persistState {
		m.finalizeAuthRateLimitStateChange(snapshot, persistState)
	}
	return true, time.Time{}
}

func (m *Manager) finalizeAuthRateLimitStateChange(snapshot *Auth, persist bool) {
	if snapshot != nil && m.scheduler != nil {
		m.scheduler.upsertAuth(snapshot)
	}
	if persist && snapshot != nil {
		m.persistAuthRateLimitState(snapshot)
	}
}

func (m *Manager) persistAuthRateLimitState(snapshot *Auth) {
	if m == nil || snapshot == nil {
		return
	}
	_ = m.persist(context.Background(), snapshot)
}
