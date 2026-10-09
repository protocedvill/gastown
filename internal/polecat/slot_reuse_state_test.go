package polecat

import "testing"

// A finished polecat keeps its sandbox for reuse and nothing transitions it back
// to idle, so `done` must count as a reuse candidate. Gating reuse on idle alone
// made finished polecats accumulate one directory each until the rig hit its
// directory cap, at which point every dispatch failed permanently.
func TestSlotReuseCandidateState(t *testing.T) {
	tests := []struct {
		state State
		want  bool
	}{
		{StateIdle, true},
		{StateDone, true},
		{StateWorking, false},
		{StateStalled, false},
		{StateReviewNeeded, false},
		{State("nonsense"), false},
	}
	for _, tt := range tests {
		if got := SlotReuseCandidateState(tt.state); got != tt.want {
			t.Errorf("SlotReuseCandidateState(%q) = %v, want %v", tt.state, got, tt.want)
		}
	}
}
