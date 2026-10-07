package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/refinery"
	"github.com/steveyegge/gastown/internal/style"
)

var mqLandCmd = &cobra.Command{
	Use:   "land <rig> <mr-id>",
	Short: "Merge an MR and run post-merge cleanup in one step",
	Long: `Land one merge request the way the refinery engine does, in a single command:

  1. Run the configured gates and merge the polecat branch into its target
  2. Push and verify the target contains the merge
  3. Post-merge cleanup: close the MR and source issue, clear the polecat's
     active_mr, delete the merged branch
  4. Nudge the mayor (MERGED)

On a conflict or gate failure the MR stays open and the polecat is told what
to fix, exactly as in the refinery's own queue processing.

Agent-driven refineries should prefer this over hand-running git merge and
gt mq post-merge: skipping the post-merge step leaves the polecat holding its
slot, which eventually blocks all dispatch.

Examples:
  gt mq land gastown gt-mr-abc123`,
	Args: cobra.ExactArgs(2),
	RunE: runMQLand,
}

func init() {
	mqCmd.AddCommand(mqLandCmd)
}

func runMQLand(cmd *cobra.Command, args []string) error {
	rigName, mrID := args[0], strings.TrimSpace(args[1])

	_, r, err := getRig(rigName)
	if err != nil {
		return err
	}
	eng := refinery.NewEngineer(r)
	if err := eng.LoadConfig(); err != nil {
		return fmt.Errorf("loading merge queue config: %w", err)
	}

	open, err := eng.ListAllOpenMRs()
	if err != nil {
		return err
	}
	var mr *refinery.MRInfo
	for _, m := range open {
		if m.ID == mrID {
			mr = m
			break
		}
	}
	if mr == nil {
		return fmt.Errorf("no open merge request %s in rig %s (see gt mq list %s)", mrID, rigName, rigName)
	}

	result := eng.ProcessMRInfo(context.Background(), mr)
	if !result.Success {
		eng.HandleMRInfoFailure(mr, result)
		return fmt.Errorf("landing %s failed: %s", mr.ID, result.Error)
	}
	if !eng.HandleMRInfoSuccess(mr, result) {
		return fmt.Errorf("%s merged at %s but post-merge cleanup failed; run gt mq post-merge %s %s", mr.ID, result.MergeCommit, rigName, mr.ID)
	}
	fmt.Printf("%s Landed %s at %s\n", style.Bold.Render("✓"), mr.ID, result.MergeCommit)
	return nil
}
