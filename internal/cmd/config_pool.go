package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/scheduler/capacity"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var configPoolCmd = &cobra.Command{
	Use:   "pool",
	Short: "Manage per-agent polecat capacity pools",
	Long: `Manage per-agent polecat capacity pools.

A pool caps how many concurrent polecats may run on one agent alias, so a town
can run several models side by side — for example four polecats on a local model
and four on a hosted one — without either starving the other.

Pools are sub-ceilings inside scheduler.max_polecats, which stays the town-wide
total. Set max_polecats to at least the sum of the pools while in deferred mode
(max_polecats > 0), or the town-wide ceiling will gate dispatch first.

An agent with no pool entry is unlimited, so a town that configures no pools
behaves exactly as it did before pools existed.

Examples:
  gt config pool set opencode-exa 4         # cap the local model's pool at 4
  gt config pool set spacebunny 4           # cap a second model's pool at 4
  gt config set scheduler.max_polecats 8    # town-wide total across both pools
  gt config pool list
  gt config pool remove spacebunny`,
}

var configPoolSetCmd = &cobra.Command{
	Use:   "set <agent> <max>",
	Short: "Set the concurrent-polecat ceiling for an agent alias",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigPoolSet,
}

var configPoolRemoveCmd = &cobra.Command{
	Use:     "remove <agent>",
	Aliases: []string{"rm", "unset"},
	Short:   "Remove an agent alias's pool ceiling",
	Args:    cobra.ExactArgs(1),
	RunE:    runConfigPoolRemove,
}

var configPoolListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured agent pools",
	Args:  cobra.NoArgs,
	RunE:  runConfigPoolList,
}

var configPoolListJSON bool

// spill flags for `gt config pool set`.
var (
	configPoolSetSpill   bool
	configPoolSetNoSpill bool
)

func stringListContains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func removeStringFromList(list []string, drop string) []string {
	var kept []string
	for _, item := range list {
		if item != drop {
			kept = append(kept, item)
		}
	}
	return kept
}

// loadTownScheduler loads town settings plus the scheduler section, defaulting
// when the town has no scheduler block yet.
func loadTownScheduler(townRoot string) (*config.TownSettings, *capacity.SchedulerConfig, string, error) {
	settingsPath := config.TownSettingsPath(townRoot)
	townSettings, err := config.LoadOrCreateTownSettings(settingsPath)
	if err != nil {
		return nil, nil, settingsPath, fmt.Errorf("loading town settings: %w", err)
	}
	schedulerCfg := townSettings.Scheduler
	if schedulerCfg == nil {
		schedulerCfg = capacity.DefaultSchedulerConfig()
	}
	return townSettings, schedulerCfg, settingsPath, nil
}

// agentPoolSum totals the configured pool ceilings.
func agentPoolSum(schedulerCfg *capacity.SchedulerConfig) int {
	sum := 0
	for _, limit := range schedulerCfg.GetAgentPools() {
		if limit > 0 {
			sum += limit
		}
	}
	return sum
}

func runConfigPoolSet(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	agent := strings.TrimSpace(args[0])
	if agent == "" {
		return fmt.Errorf("agent alias must not be empty")
	}
	limit, err := strconv.Atoi(strings.TrimSpace(args[1]))
	if err != nil {
		return fmt.Errorf("invalid pool size %q: expected a positive integer", args[1])
	}
	if limit <= 0 {
		return fmt.Errorf("invalid pool size %d: expected a positive integer (use 'gt config pool remove %s' to unset)", limit, agent)
	}

	townSettings, schedulerCfg, settingsPath, err := loadTownScheduler(townRoot)
	if err != nil {
		return err
	}
	if schedulerCfg.AgentPools == nil {
		schedulerCfg.AgentPools = map[string]int{}
	}
	schedulerCfg.AgentPools[agent] = limit
	switch {
	case configPoolSetSpill && !stringListContains(schedulerCfg.AgentPoolSpill, agent):
		schedulerCfg.AgentPoolSpill = append(schedulerCfg.AgentPoolSpill, agent)
	case configPoolSetNoSpill:
		schedulerCfg.AgentPoolSpill = removeStringFromList(schedulerCfg.AgentPoolSpill, agent)
	}
	if len(schedulerCfg.AgentPoolSpill) == 0 {
		schedulerCfg.AgentPoolSpill = nil
	}
	townSettings.Scheduler = schedulerCfg
	if err := config.SaveTownSettings(settingsPath, townSettings); err != nil {
		return fmt.Errorf("saving town settings: %w", err)
	}

	fmt.Printf("%s Pool %s = %d concurrent polecat(s)\n", style.Bold.Render("✓"), style.Bold.Render(agent), limit)
	if stringListContains(schedulerCfg.AgentPoolSpill, agent) {
		fmt.Printf("  spill: unassigned beads may run on %s once higher-priority pools are full\n", agent)
	}

	// Guidance only — an under-sized town-wide cap silently wins over the pools,
	// and a direct-dispatch town never consults them during dispatch at all.
	total := schedulerCfg.GetMaxPolecats()
	switch {
	case total <= 0:
		fmt.Printf("  %s scheduler.max_polecats is %d (direct dispatch): pools gate only directly-slung polecats. Set it to the pool total to schedule against them.\n",
			style.Warning.Render("⚠"), total)
	case agentPoolSum(schedulerCfg) > total:
		fmt.Printf("  %s pools sum to %d but scheduler.max_polecats is %d — raise it: gt config set scheduler.max_polecats %d\n",
			style.Warning.Render("⚠"), agentPoolSum(schedulerCfg), total, agentPoolSum(schedulerCfg))
	}
	return nil
}

func runConfigPoolRemove(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	agent := strings.TrimSpace(args[0])
	if agent == "" {
		return fmt.Errorf("agent alias must not be empty")
	}

	townSettings, schedulerCfg, settingsPath, err := loadTownScheduler(townRoot)
	if err != nil {
		return err
	}
	pools := schedulerCfg.GetAgentPools()
	if _, ok := pools[agent]; !ok {
		return fmt.Errorf("no pool configured for agent %q", agent)
	}
	delete(schedulerCfg.AgentPools, agent)
	if len(schedulerCfg.AgentPools) == 0 {
		schedulerCfg.AgentPools = nil
	}
	schedulerCfg.AgentPoolSpill = removeStringFromList(schedulerCfg.AgentPoolSpill, agent)
	if len(schedulerCfg.AgentPoolSpill) == 0 {
		schedulerCfg.AgentPoolSpill = nil
	}
	townSettings.Scheduler = schedulerCfg
	if err := config.SaveTownSettings(settingsPath, townSettings); err != nil {
		return fmt.Errorf("saving town settings: %w", err)
	}

	fmt.Printf("%s Pool %s removed (now unlimited)\n", style.Bold.Render("✓"), style.Bold.Render(agent))
	return nil
}

func runConfigPoolList(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwd()
	if err != nil {
		return fmt.Errorf("finding town root: %w", err)
	}

	_, schedulerCfg, _, err := loadTownScheduler(townRoot)
	if err != nil {
		return err
	}
	pools := schedulerCfg.GetAgentPools()

	if configPoolListJSON {
		out := map[string]any{
			"pools":           pools,
			"spill":           schedulerCfg.GetAgentPoolSpill(),
			"max_polecats":    schedulerCfg.GetMaxPolecats(),
			"pooled_total":    agentPoolSum(schedulerCfg),
			"deferred":        schedulerCfg.IsDeferred(),
			"unlisted_policy": "unlimited",
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	}

	if len(pools) == 0 {
		fmt.Println("No agent pools configured — every agent is unlimited.")
		return nil
	}

	agents := make([]string, 0, len(pools))
	for agent := range pools {
		agents = append(agents, agent)
	}
	sort.Strings(agents)

	fmt.Printf("Agent pools (town-wide total: %d, mode: %s)\n", schedulerCfg.GetMaxPolecats(), dispatchModeLabel(schedulerCfg))
	spill := schedulerCfg.GetAgentPoolSpill()
	for _, agent := range agents {
		marker := ""
		if stringListContains(spill, agent) {
			marker = "  (spill)"
		}
		fmt.Printf("  %-24s %d%s\n", agent, pools[agent], marker)
	}
	fmt.Printf("\nPooled total: %d — agents with no entry above are unlimited.\n", agentPoolSum(schedulerCfg))
	if len(spill) > 0 {
		fmt.Printf("Unassigned beads fill the default agent first, then spill to: %s\n", strings.Join(spill, ", "))
	} else {
		fmt.Println("No spill configured — unassigned beads stay on the default agent, so pools are pure ceilings.")
	}
	return nil
}

func dispatchModeLabel(schedulerCfg *capacity.SchedulerConfig) string {
	if schedulerCfg.IsDeferred() {
		return "deferred dispatch"
	}
	return "direct dispatch"
}

func init() {
	configPoolCmd.AddCommand(configPoolSetCmd)
	configPoolCmd.AddCommand(configPoolRemoveCmd)
	configPoolCmd.AddCommand(configPoolListCmd)
	configPoolListCmd.Flags().BoolVar(&configPoolListJSON, "json", false, "Output as JSON")
	configPoolSetCmd.Flags().BoolVar(&configPoolSetSpill, "spill", false, "Also let unassigned beads run on this agent once higher-priority pools are full")
	configPoolSetCmd.Flags().BoolVar(&configPoolSetNoSpill, "no-spill", false, "Stop letting unassigned beads run on this agent")
	configCmd.AddCommand(configPoolCmd)
}
