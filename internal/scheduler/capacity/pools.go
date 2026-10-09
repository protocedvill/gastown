package capacity

import "math"

// PoolUsage tracks per-agent polecat occupancy against per-agent ceilings.
//
// Pools are opt-in: an agent with no entry in Limits is unlimited, so a town
// that configures no pools behaves exactly as it did before pools existed.
// A limit <= 0 is treated as "unlimited" for the same reason — a malformed
// limit must not wedge the scheduler.
//
// The zero value is usable: no limits, nothing in use.
type PoolUsage struct {
	// Limits maps agent alias -> max concurrent polecats for that agent.
	Limits map[string]int
	// InUse maps agent alias -> currently occupied slots for that agent.
	InUse map[string]int
}

// NewPoolUsage builds a PoolUsage from configured limits and current occupancy.
func NewPoolUsage(limits, inUse map[string]int) *PoolUsage {
	return &PoolUsage{Limits: limits, InUse: inUse}
}

// Unlimited is returned by Remaining for agents that have no configured ceiling.
const Unlimited = math.MaxInt

// LimitFor returns the configured ceiling for an agent, or Unlimited.
func (u *PoolUsage) LimitFor(agent string) int {
	if u == nil || len(u.Limits) == 0 {
		return Unlimited
	}
	limit, ok := u.Limits[agent]
	if !ok || limit <= 0 {
		return Unlimited
	}
	return limit
}

// Remaining returns how many more slots the agent may take. Agents with no
// configured ceiling return Unlimited. The value can be <= 0 when a pool is
// full or over-admitted (e.g. limits were lowered while polecats were running).
func (u *PoolUsage) Remaining(agent string) int {
	limit := u.LimitFor(agent)
	if limit == Unlimited {
		return Unlimited
	}
	used := 0
	if u != nil {
		used = u.InUse[agent]
	}
	return limit - used
}

// TryAcquire consumes one slot for the agent, reporting false when its pool is
// already at its ceiling. Agents with no configured ceiling always succeed.
// The consumption is in-memory and scoped to one planning pass: it lets a
// single plan fill a pool without over-committing it.
func (u *PoolUsage) TryAcquire(agent string) bool {
	if u == nil || u.Remaining(agent) <= 0 {
		return u == nil || u.LimitFor(agent) == Unlimited
	}
	if u.InUse == nil {
		u.InUse = map[string]int{}
	}
	u.InUse[agent]++
	return true
}

// PlanDispatchWithLimits is PlanDispatch extended with per-agent pool ceilings.
//
// agentOf resolves the agent alias a pending bead will run as (the bead's sling
// override, or the town default when it has none). pools carries the ceilings
// and current occupancy; a slot is taken from the bead's pool as each bead is
// planned, so one pass cannot over-commit a pool.
//
// Beads whose pool is full are skipped rather than planned. Skipping (not
// failing) is deliberate: a dispatch failure consumes the bead's failure quota
// and can circuit-break it, which would silently drop work whenever a pool
// stayed saturated. Beads skipped for pool reasons report reason
// "pool-capacity" so the skip is visible in scheduler output.
//
// The global capacity argument still bounds the cycle; pools are sub-ceilings
// inside it.
func PlanDispatchWithLimits(totalCapacity, batchSize int, ready []PendingBead,
	agentOf func(PendingBead) string, pools *PoolUsage) DispatchPlan {
	ready, msgSkipped := FilterMessagingBeads(ready)

	if len(ready) == 0 {
		if msgSkipped > 0 {
			return DispatchPlan{Skipped: msgSkipped, Reason: "messaging-filtered"}
		}
		return DispatchPlan{Reason: "none"}
	}

	if totalCapacity <= 0 {
		return DispatchPlan{
			Skipped: len(ready) + msgSkipped,
			Reason:  "capacity",
		}
	}

	budget := batchSize
	if totalCapacity < budget {
		budget = totalCapacity
	}

	var toDispatch []PendingBead
	poolSkipped := 0
	for _, b := range ready {
		if len(toDispatch) >= budget {
			break
		}
		agent := ""
		if agentOf != nil {
			agent = agentOf(b)
		}
		if pools != nil && !pools.TryAcquire(agent) {
			poolSkipped++
			continue
		}
		toDispatch = append(toDispatch, b)
	}

	if len(toDispatch) == 0 {
		reason := "capacity"
		if poolSkipped > 0 && totalCapacity > 0 {
			reason = "pool-capacity"
		}
		plan := DispatchPlan{Skipped: len(ready) + msgSkipped, Reason: reason}
		if poolSkipped > 0 {
			plan.Reason = "pool-capacity"
		}
		return plan
	}

	skipped := len(ready) - len(toDispatch) + msgSkipped
	reason := "batch"
	switch {
	case poolSkipped > 0 && len(ready) > len(toDispatch):
		reason = "batch+pool-capacity"
	case poolSkipped > 0:
		reason = "pool-capacity"
	case totalCapacity < batchSize && totalCapacity < len(ready):
		reason = "capacity"
	case len(ready) < batchSize && len(ready) < totalCapacity:
		reason = "ready"
	}
	if msgSkipped > 0 {
		reason = reason + "+messaging-filtered"
	}

	return DispatchPlan{ToDispatch: toDispatch, Skipped: skipped, Reason: reason}
}
