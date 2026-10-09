package capacity

import "testing"

func pendingWithAgent(id, agent string) PendingBead {
	b := PendingBead{ID: id, WorkBeadID: "w-" + id}
	if agent != "" {
		b.Context = &SlingContextFields{Agent: agent}
	}
	return b
}

func TestPoolUsageRemaining(t *testing.T) {
	tests := []struct {
		name  string
		pools *PoolUsage
		agent string
		want  int
	}{
		{"nil pools are unlimited", nil, "anything", Unlimited},
		{"no limits configured is unlimited", &PoolUsage{}, "anything", Unlimited},
		{"unlisted agent is unlimited", &PoolUsage{Limits: map[string]int{"a": 2}}, "b", Unlimited},
		{"non-positive limit is treated as unlimited", &PoolUsage{Limits: map[string]int{"a": 0}}, "a", Unlimited},
		{"listed agent reports headroom", &PoolUsage{Limits: map[string]int{"a": 3}, InUse: map[string]int{"a": 1}}, "a", 2},
		{"full pool reports zero", &PoolUsage{Limits: map[string]int{"a": 2}, InUse: map[string]int{"a": 2}}, "a", 0},
		{"over-admitted pool goes negative", &PoolUsage{Limits: map[string]int{"a": 1}, InUse: map[string]int{"a": 3}}, "a", -2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pools.Remaining(tt.agent); got != tt.want {
				t.Errorf("Remaining(%q) = %d, want %d", tt.agent, got, tt.want)
			}
		})
	}
}

func TestPoolUsageTryAcquire(t *testing.T) {
	pools := &PoolUsage{Limits: map[string]int{"a": 2}}

	if !pools.TryAcquire("a") || !pools.TryAcquire("a") {
		t.Fatal("first two acquisitions for a 2-slot pool should succeed")
	}
	if pools.TryAcquire("a") {
		t.Error("third acquisition should fail: pool is full")
	}
	// Unlisted agents are never refused.
	if !pools.TryAcquire("b") || !pools.TryAcquire("b") || !pools.TryAcquire("b") {
		t.Error("unlisted agent should never be refused")
	}
	if got := pools.InUse["a"]; got != 2 {
		t.Errorf("InUse[a] = %d, want 2 (failed acquire must not consume)", got)
	}
}

func TestPlanDispatchWithLimitsSplitsByAgent(t *testing.T) {
	ready := []PendingBead{
		pendingWithAgent("a1", "strata"),
		pendingWithAgent("a2", "strata"),
		pendingWithAgent("b1", "bunny"),
		pendingWithAgent("b2", "bunny"),
	}
	// strata is saturated; bunny has room.
	pools := &PoolUsage{Limits: map[string]int{"strata": 2, "bunny": 2}, InUse: map[string]int{"strata": 2}}
	agentOf := func(b PendingBead) string { return b.Context.Agent }

	plan := PlanDispatchWithLimits(8, 8, ready, agentOf, pools)

	if len(plan.ToDispatch) != 2 {
		t.Fatalf("ToDispatch = %d (%v), want 2", len(plan.ToDispatch), plan.ToDispatch)
	}
	for _, b := range plan.ToDispatch {
		if b.ID != "b1" && b.ID != "b2" {
			t.Errorf("dispatched %q; a full pool's beads must be skipped", b.ID)
		}
	}
	if plan.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", plan.Skipped)
	}
}

func TestPlanDispatchWithLimitsFillsPoolWithinOnePass(t *testing.T) {
	ready := []PendingBead{
		pendingWithAgent("a1", "strata"),
		pendingWithAgent("a2", "strata"),
		pendingWithAgent("a3", "strata"),
	}
	pools := &PoolUsage{Limits: map[string]int{"strata": 2}}
	agentOf := func(b PendingBead) string { return b.Context.Agent }

	plan := PlanDispatchWithLimits(8, 8, ready, agentOf, pools)

	if len(plan.ToDispatch) != 2 {
		t.Fatalf("ToDispatch = %d, want 2 (pool ceiling applies within a single pass)", len(plan.ToDispatch))
	}
	if plan.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", plan.Skipped)
	}
}

func TestPlanDispatchWithLimitsAllPoolsFull(t *testing.T) {
	ready := []PendingBead{
		pendingWithAgent("a1", "strata"),
		pendingWithAgent("b1", "bunny"),
	}
	pools := &PoolUsage{
		Limits: map[string]int{"strata": 1, "bunny": 1},
		InUse:  map[string]int{"strata": 1, "bunny": 1},
	}
	agentOf := func(b PendingBead) string { return b.Context.Agent }

	plan := PlanDispatchWithLimits(8, 8, ready, agentOf, pools)

	if len(plan.ToDispatch) != 0 {
		t.Fatalf("ToDispatch = %d, want 0", len(plan.ToDispatch))
	}
	if plan.Reason != "pool-capacity" {
		t.Errorf("Reason = %q, want %q", plan.Reason, "pool-capacity")
	}
	if plan.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", plan.Skipped)
	}
}

func TestPlanDispatchWithLimitsUnlistedAgentIsUnlimited(t *testing.T) {
	ready := []PendingBead{
		pendingWithAgent("a1", "strata"),
		pendingWithAgent("a2", "strata"),
		pendingWithAgent("a3", "strata"),
	}
	pools := &PoolUsage{Limits: map[string]int{"bunny": 1}, InUse: map[string]int{"bunny": 1}}
	agentOf := func(b PendingBead) string { return b.Context.Agent }

	plan := PlanDispatchWithLimits(8, 8, ready, agentOf, pools)

	if len(plan.ToDispatch) != 3 {
		t.Errorf("ToDispatch = %d, want 3 (an unlisted agent has no ceiling)", len(plan.ToDispatch))
	}
}

func TestPlanDispatchWithLimitsNoPoolsMatchesPlanDispatch(t *testing.T) {
	ready := []PendingBead{
		pendingWithAgent("a1", "strata"),
		pendingWithAgent("b1", "bunny"),
		pendingWithAgent("c1", "strata"),
	}
	agentOf := func(b PendingBead) string { return b.Context.Agent }

	withPools := PlanDispatchWithLimits(2, 3, ready, agentOf, &PoolUsage{})
	without := PlanDispatch(2, 3, ready)

	if len(withPools.ToDispatch) != len(without.ToDispatch) {
		t.Fatalf("ToDispatch mismatch: %d vs %d", len(withPools.ToDispatch), len(without.ToDispatch))
	}
	if withPools.Reason != without.Reason {
		t.Errorf("Reason = %q, want %q", withPools.Reason, without.Reason)
	}
	if withPools.Skipped != without.Skipped {
		t.Errorf("Skipped = %d, want %d", withPools.Skipped, without.Skipped)
	}
}

func TestPlanDispatchWithLimitsGlobalCapacityStillWins(t *testing.T) {
	ready := []PendingBead{
		pendingWithAgent("a1", "strata"),
		pendingWithAgent("b1", "bunny"),
	}
	pools := &PoolUsage{Limits: map[string]int{"strata": 4, "bunny": 4}}
	agentOf := func(b PendingBead) string { return b.Context.Agent }

	plan := PlanDispatchWithLimits(0, 4, ready, agentOf, pools)

	if len(plan.ToDispatch) != 0 {
		t.Errorf("ToDispatch = %d, want 0 when the town-wide budget is exhausted", len(plan.ToDispatch))
	}
}

func TestSchedulerConfigAgentPools(t *testing.T) {
	cfg := &SchedulerConfig{AgentPools: map[string]int{"strata": 4, "bunny": 0}}

	if !cfg.HasAgentPools() {
		t.Error("HasAgentPools() = false, want true")
	}
	if limit, ok := cfg.GetAgentPoolLimit("strata"); !ok || limit != 4 {
		t.Errorf("GetAgentPoolLimit(strata) = (%d, %v), want (4, true)", limit, ok)
	}
	if _, ok := cfg.GetAgentPoolLimit("bunny"); ok {
		t.Error("a non-positive limit must not count as a configured ceiling")
	}
	if _, ok := cfg.GetAgentPoolLimit("unlisted"); ok {
		t.Error("unlisted agent must have no ceiling")
	}

	var nilCfg *SchedulerConfig
	if nilCfg.HasAgentPools() {
		t.Error("nil config must report no pools")
	}
	if nilCfg.GetAgentPools() != nil {
		t.Error("nil config must return nil pools")
	}
}
