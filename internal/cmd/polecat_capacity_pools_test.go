package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/scheduler/capacity"
)

// setupAgentPoolTestTown writes a town whose scheduler has per-agent pools.
func setupAgentPoolTestTown(t *testing.T, pools map[string]int, maxPolecats int) string {
	t.Helper()
	townRoot := t.TempDir()
	batchSize := 1
	settings := config.NewTownSettings()
	settings.Scheduler = &capacity.SchedulerConfig{
		MaxPolecats: &maxPolecats,
		BatchSize:   &batchSize,
		AgentPools:  pools,
	}
	writeJSONFile(t, config.TownSettingsPath(townRoot), settings)
	if err := config.SaveRigsConfig(filepath.Join(townRoot, "mayor", "rigs.json"),
		&config.RigsConfig{Version: config.CurrentRigsVersion}); err != nil {
		t.Fatalf("SaveRigsConfig: %v", err)
	}
	return townRoot
}

func releaseAdmission(handle *polecatAdmissionHandle) {
	if handle != nil {
		handle.Release()
	}
}

func TestAcquirePolecatAdmissionHonoursAgentPools(t *testing.T) {
	townRoot := setupAgentPoolTestTown(t, map[string]int{"strata": 2, "bunny": 1}, 8)

	first, _, err := acquirePolecatAdmission(townRoot, "gastown", "gt-one", "test", "strata")
	if err != nil {
		t.Fatalf("first strata admission: %v", err)
	}
	defer releaseAdmission(first)

	second, snapshot, err := acquirePolecatAdmission(townRoot, "gastown", "gt-two", "test", "strata")
	if err != nil {
		t.Fatalf("second strata admission: %v", err)
	}
	defer releaseAdmission(second)

	if got := snapshot.ByAgent["strata"]; got != 2 {
		t.Fatalf("ByAgent[strata] = %d, want 2", got)
	}
	if snapshot.Free != 6 {
		t.Fatalf("Free = %d, want 6 (2 of 8 town-wide slots used)", snapshot.Free)
	}

	// The town-wide budget still has room; the pool does not.
	third, denied, err := acquirePolecatAdmission(townRoot, "gastown", "gt-three", "test", "strata")
	if third != nil {
		defer releaseAdmission(third)
	}
	if err == nil {
		t.Fatal("third strata admission should be refused by the pool ceiling")
	}
	var admissionErr *polecatCapacityAdmissionError
	if !errors.As(err, &admissionErr) {
		t.Fatalf("error = %v, want *polecatCapacityAdmissionError", err)
	}
	if !strings.Contains(err.Error(), "agent pool") {
		t.Errorf("error = %q, want it to name the agent pool", err.Error())
	}
	if denied.Max != 8 || denied.Free != 6 {
		t.Errorf("denied snapshot = max %d free %d, want max 8 free 6 (town-wide budget untouched)", denied.Max, denied.Free)
	}

	// A different agent keeps its own separate headroom.
	fourth, poolSnapshot, err := acquirePolecatAdmission(townRoot, "gastown", "gt-four", "test", "bunny")
	if err != nil {
		t.Fatalf("bunny admission: %v", err)
	}
	defer releaseAdmission(fourth)
	if got := poolSnapshot.ByAgent["bunny"]; got != 1 {
		t.Errorf("ByAgent[bunny] = %d, want 1", got)
	}
	// bunny's ceiling is 1, so a second bunny is refused too.
	fifth, _, err := acquirePolecatAdmission(townRoot, "gastown", "gt-five", "test", "bunny")
	if fifth != nil {
		defer releaseAdmission(fifth)
	}
	if err == nil {
		t.Error("second bunny admission should be refused (pool ceiling 1)")
	}

	// An agent with no pool entry stays unlimited.
	for i := 0; i < 3; i++ {
		handle, _, err := acquirePolecatAdmission(townRoot, "gastown", "gt-other", "test", "unlisted-agent")
		if err != nil {
			t.Fatalf("unlisted agent admission %d: %v", i, err)
		}
		defer releaseAdmission(handle)
	}
}

func TestAcquirePolecatAdmissionWithoutPoolsIsUnchanged(t *testing.T) {
	townRoot := setupAgentPoolTestTown(t, nil, 2)

	a, _, err := acquirePolecatAdmission(townRoot, "gastown", "gt-one", "test", "strata")
	if err != nil {
		t.Fatalf("first admission: %v", err)
	}
	defer releaseAdmission(a)

	b, snapshot, err := acquirePolecatAdmission(townRoot, "gastown", "gt-two", "test", "strata")
	if err != nil {
		t.Fatalf("second admission: %v", err)
	}
	defer releaseAdmission(b)
	if snapshot.ByAgent["strata"] != 2 {
		t.Errorf("ByAgent = %v, want strata:2 (occupancy is tracked even without pools)", snapshot.ByAgent)
	}

	// The refusal must come from the town-wide cap, not from a pool.
	_, _, err = acquirePolecatAdmission(townRoot, "gastown", "gt-three", "test", "strata")
	if err == nil {
		t.Fatal("expected the town-wide capacity cap to refuse the third polecat")
	}
	if strings.Contains(err.Error(), "agent pool") {
		t.Errorf("no pools are configured, so the error must not mention one: %v", err)
	}
}

func TestAcquirePolecatAdmissionChecksDefaultAgentPoolWhenAgentUnset(t *testing.T) {
	townRoot := t.TempDir()
	maxPolecats, batchSize := 8, 1
	settings := config.NewTownSettings()
	settings.DefaultAgent = "strata"
	settings.Scheduler = &capacity.SchedulerConfig{
		MaxPolecats: &maxPolecats,
		BatchSize:   &batchSize,
		AgentPools:  map[string]int{"strata": 1},
	}
	writeJSONFile(t, config.TownSettingsPath(townRoot), settings)
	if err := config.SaveRigsConfig(filepath.Join(townRoot, "mayor", "rigs.json"),
		&config.RigsConfig{Version: config.CurrentRigsVersion}); err != nil {
		t.Fatalf("SaveRigsConfig: %v", err)
	}

	first, _, err := acquirePolecatAdmission(townRoot, "gastown", "gt-one", "test", "")
	if err != nil {
		t.Fatalf("first admission: %v", err)
	}
	defer releaseAdmission(first)

	// A direct sling with no --agent runs the town default, so the default
	// agent's pool must gate it rather than letting it slip through uncharged.
	second, _, err := acquirePolecatAdmission(townRoot, "gastown", "gt-two", "test", "")
	if second != nil {
		defer releaseAdmission(second)
	}
	if err == nil {
		t.Fatal("an unset agent must be charged to the default agent's pool")
	}
	if !strings.Contains(err.Error(), "agent pool") {
		t.Errorf("error = %v, want a pool refusal", err)
	}
}
