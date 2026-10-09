// Package capacity provides types and pure functions for the capacity-controlled
// dispatch scheduler. The impure orchestration (dispatch loop, enqueue, epic/convoy
// resolution) stays in cmd but uses types and pure functions from this package.
package capacity

import "time"

// SchedulerConfig configures the capacity scheduler for polecat dispatch.
// This is a town-wide setting (not per-rig) because capacity control is host-wide:
// API rate limits, memory, and CPU are shared resources across all rigs.
//
// Behavior is driven entirely by MaxPolecats:
//
//	-1 (default): direct dispatch — gt sling works as before, near-zero overhead
//	 0:           direct dispatch (same as -1)
//	 N > 0:       deferred dispatch — labels/metadata applied, daemon dispatches
type SchedulerConfig struct {
	// MaxPolecats is the max concurrent polecats across ALL rigs.
	// Includes both scheduler-dispatched and directly-slung polecats.
	// nil/absent = default (-1, direct dispatch). 0 = direct dispatch (same as -1).
	// N > 0 = deferred dispatch with capacity control.
	MaxPolecats *int `json:"max_polecats,omitempty"`

	// BatchSize is the number of beads to dispatch per heartbeat tick.
	// Limits spawn rate per 3-minute cycle.
	// nil/absent = default (1). Explicit 0 is rejected by config setter.
	BatchSize *int `json:"batch_size,omitempty"`

	// SpawnDelay is the delay between spawns to prevent Dolt lock contention.
	// Default: "0s".
	SpawnDelay string `json:"spawn_delay,omitempty"`

	// AgentPools caps concurrent polecats per agent alias, so a town can run
	// several models side by side (e.g. 4 on a local model, 4 on a hosted one)
	// without either starving the other.
	//
	// Maps agent alias -> max concurrent polecats for that agent. Only
	// meaningful in deferred dispatch (MaxPolecats > 0); directly-slung polecats
	// still respect their pool at admission.
	//
	// Opt-in: an agent with no entry here is unlimited, so a town that sets no
	// pools behaves exactly as before. A limit <= 0 is treated as unlimited.
	// Pools are sub-ceilings inside MaxPolecats, which stays the town-wide total.
	AgentPools map[string]int `json:"agent_pools,omitempty"`

	// AgentPoolSpill lists agent aliases that may take work whose sling names no
	// agent, in priority order. It is what lets a town actually fill a second
	// pool: unassigned beads prefer the default agent's pool and then spill to
	// the pools listed here.
	//
	// Empty by default, which means unassigned beads never leave the default
	// agent — a pool is then a pure ceiling, which is what you want for capping
	// an expensive model.
	AgentPoolSpill []string `json:"agent_pool_spill,omitempty"`
}

// DefaultSchedulerConfig returns a SchedulerConfig with sensible defaults.
// MaxPolecats=-1 means direct dispatch (no scheduler overhead).
func DefaultSchedulerConfig() *SchedulerConfig {
	defaultMax := -1
	defaultBatch := 1
	return &SchedulerConfig{
		MaxPolecats: &defaultMax,
		BatchSize:   &defaultBatch,
		SpawnDelay:  "0s",
	}
}

// GetMaxPolecats returns MaxPolecats or the default (-1, direct dispatch) if unset.
func (c *SchedulerConfig) GetMaxPolecats() int {
	if c == nil || c.MaxPolecats == nil {
		return -1
	}
	return *c.MaxPolecats
}

// GetBatchSize returns BatchSize or the default (1) if unset.
func (c *SchedulerConfig) GetBatchSize() int {
	if c == nil || c.BatchSize == nil {
		return 1
	}
	return *c.BatchSize
}

// GetSpawnDelay returns SpawnDelay as a duration, defaulting to 0s.
func (c *SchedulerConfig) GetSpawnDelay() time.Duration {
	if c == nil || c.SpawnDelay == "" {
		return 0
	}
	return ParseDurationOrDefault(c.SpawnDelay, 0)
}

// IsDeferred returns true when the scheduler is configured for deferred dispatch
// (max_polecats > 0). Returns false for direct dispatch (-1) and disabled (0).
func (c *SchedulerConfig) IsDeferred() bool {
	return c.GetMaxPolecats() > 0
}

// GetAgentPools returns the per-agent polecat ceilings, or nil when unset.
func (c *SchedulerConfig) GetAgentPools() map[string]int {
	if c == nil || len(c.AgentPools) == 0 {
		return nil
	}
	return c.AgentPools
}

// HasAgentPools reports whether any per-agent ceilings are configured.
func (c *SchedulerConfig) HasAgentPools() bool {
	return len(c.GetAgentPools()) > 0
}

// GetAgentPoolLimit returns the configured ceiling for one agent alias, and
// whether that agent has a finite ceiling at all.
func (c *SchedulerConfig) GetAgentPoolLimit(agent string) (int, bool) {
	limit, ok := c.GetAgentPools()[agent]
	if !ok || limit <= 0 {
		return 0, false
	}
	return limit, true
}

// GetAgentPoolSpill returns the ordered aliases eligible for unassigned work.
func (c *SchedulerConfig) GetAgentPoolSpill() []string {
	if c == nil || len(c.AgentPoolSpill) == 0 {
		return nil
	}
	return c.AgentPoolSpill
}

// CandidateAgentsForUnassigned returns the agents an unassigned bead may run as,
// in priority order: the town default first, then the configured spill list.
// Callers get an empty slice when nothing is configured, meaning "leave the
// bead on the town default, whatever that is".
func (c *SchedulerConfig) CandidateAgentsForUnassigned(defaultAgent string) []string {
	var candidates []string
	add := func(agent string) {
		if agent == "" {
			return
		}
		for _, existing := range candidates {
			if existing == agent {
				return
			}
		}
		candidates = append(candidates, agent)
	}
	add(defaultAgent)
	for _, agent := range c.GetAgentPoolSpill() {
		add(agent)
	}
	return candidates
}

// ParseDurationOrDefault parses a Go duration string, returning fallback on error or empty input.
func ParseDurationOrDefault(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}
