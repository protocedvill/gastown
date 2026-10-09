package cmd

import (
	"testing"

	"github.com/steveyegge/gastown/internal/scheduler/capacity"
)

func poolTestSchedulerConfig(maxPolecats int, pools map[string]int, spill ...string) *capacity.SchedulerConfig {
	return &capacity.SchedulerConfig{
		MaxPolecats:    &maxPolecats,
		AgentPools:     pools,
		AgentPoolSpill: spill,
	}
}

func unassignedBead(id string) capacity.PendingBead {
	return capacity.PendingBead{ID: id, WorkBeadID: "w-" + id}
}

func pinnedBead(id, agent string) capacity.PendingBead {
	return capacity.PendingBead{
		ID: id, WorkBeadID: "w-" + id,
		Context: &capacity.SlingContextFields{Agent: agent},
	}
}

func dispatchedIDs(plan capacity.DispatchPlan) map[string]bool {
	ids := make(map[string]bool, len(plan.ToDispatch))
	for _, b := range plan.ToDispatch {
		ids[b.ID] = true
	}
	return ids
}

func TestPlanDispatchSpillsUnassignedBeadsToSecondPool(t *testing.T) {
	cfg := poolTestSchedulerConfig(8, map[string]int{"strata": 2, "bunny": 2}, "bunny")
	snapshot := polecatCapacitySnapshot{Max: 8, Free: 8}
	ready := []capacity.PendingBead{
		unassignedBead("b1"), unassignedBead("b2"), unassignedBead("b3"), unassignedBead("b4"),
	}

	plan, assigned := planDispatchWithAgentPools(snapshot, 8, ready, cfg, "strata")

	if len(plan.ToDispatch) != 4 {
		t.Fatalf("ToDispatch = %d (%v), want 4 (2 strata + 2 spilled to bunny)", len(plan.ToDispatch), dispatchedIDs(plan))
	}
	// Every planned bead carries the agent the planner reserved for it: the first
	// two stay on the default pool, the last two spill to bunny.
	want := map[string]string{"b1": "strata", "b2": "strata", "b3": "bunny", "b4": "bunny"}
	for id, agent := range want {
		if assigned[id] != agent {
			t.Errorf("assigned[%s] = %q, want %q (assignments: %v)", id, assigned[id], agent, assigned)
		}
	}
}

func TestPlanDispatchWithoutSpillKeepsUnassignedBeadsOnDefaultAgent(t *testing.T) {
	cfg := poolTestSchedulerConfig(8, map[string]int{"strata": 2, "bunny": 2})
	snapshot := polecatCapacitySnapshot{Max: 8, Free: 8}
	ready := []capacity.PendingBead{
		unassignedBead("b1"), unassignedBead("b2"), unassignedBead("b3"), unassignedBead("b4"),
	}

	plan, _ := planDispatchWithAgentPools(snapshot, 8, ready, cfg, "strata")

	if len(plan.ToDispatch) != 2 {
		t.Fatalf("ToDispatch = %d (%v), want 2 (bunny is a pure ceiling without spill)",
			len(plan.ToDispatch), dispatchedIDs(plan))
	}
	if plan.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", plan.Skipped)
	}
}

func TestPlanDispatchPinnedBeadWaitsForItsOwnPool(t *testing.T) {
	cfg := poolTestSchedulerConfig(8, map[string]int{"strata": 1, "bunny": 4}, "bunny")
	// strata is saturated; bunny is wide open.
	snapshot := polecatCapacitySnapshot{Max: 8, Free: 7, ByAgent: map[string]int{"strata": 1}}
	ready := []capacity.PendingBead{
		pinnedBead("pin", "strata"), // must wait, even though bunny has room
		unassignedBead("free1"),
		unassignedBead("free2"),
	}

	plan, _ := planDispatchWithAgentPools(snapshot, 8, ready, cfg, "strata")
	ids := dispatchedIDs(plan)

	if ids["pin"] {
		t.Error("a bead pinned with --agent strata must not be moved to another pool")
	}
	if !ids["free1"] || !ids["free2"] {
		t.Errorf("unassigned beads should spill into the free pool: %v", ids)
	}
}

func TestPlanDispatchUnlimitedDefaultAgentNeverSpills(t *testing.T) {
	// The default agent has no ceiling, so there is never pressure to spill.
	cfg := poolTestSchedulerConfig(8, map[string]int{"bunny": 2}, "bunny")
	snapshot := polecatCapacitySnapshot{Max: 8, Free: 8}
	ready := []capacity.PendingBead{
		unassignedBead("b1"), unassignedBead("b2"), unassignedBead("b3"),
	}

	plan, _ := planDispatchWithAgentPools(snapshot, 8, ready, cfg, "strata")

	if len(plan.ToDispatch) != 3 {
		t.Errorf("ToDispatch = %d, want 3 (an uncapped default agent absorbs everything)", len(plan.ToDispatch))
	}
}

func TestPlanDispatchPoolsAllFullWithNoDefaultAgent(t *testing.T) {
	// No default agent and every pool saturated: nothing can run, and the plan
	// must report the pool skip rather than silently dropping the beads.
	cfg := poolTestSchedulerConfig(8, map[string]int{"strata": 1, "bunny": 1}, "bunny")
	snapshot := polecatCapacitySnapshot{Max: 8, Free: 6, ByAgent: map[string]int{"strata": 1, "bunny": 1}}
	ready := []capacity.PendingBead{unassignedBead("b1"), unassignedBead("b2")}

	plan, _ := planDispatchWithAgentPools(snapshot, 8, ready, cfg, "")

	if len(plan.ToDispatch) != 0 {
		t.Fatalf("ToDispatch = %d, want 0", len(plan.ToDispatch))
	}
	if plan.Reason != "pool-capacity" {
		t.Errorf("Reason = %q, want pool-capacity", plan.Reason)
	}
}
