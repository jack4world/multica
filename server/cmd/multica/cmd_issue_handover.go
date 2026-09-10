package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/auditmode"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
)

// The agent handover.
//
// An agent CAN reach the same state with `issue status <id> agent_delivered` —
// the gate governs both identically, and adding a second write path into the
// review chain would be a second thing to keep in step with it.
//
// What this adds is a NAMED ACT WITH A USEFUL REFUSAL. A raw status change is a
// guess that fails with a transition error, and an agent reading that error
// retries. `handover` is a request the CLI can explain: a handover is only
// available from 编制中, only an agent can perform one, and this issue may not
// be a workpaper at all. The reader here is a machine that will otherwise loop.

var issueHandoverCmd = &cobra.Command{
	Use:   "handover <id>",
	Short: "Hand a drafted workpaper over to its preparer",
	Long: "Hand a workpaper's draft over for a human to adopt.\n\n" +
		"Only for issues inside an audit engagement. Finishing a run is NOT a handover:\n" +
		"until you hand over, the workpaper sits in 编制中 and nobody knows the draft is\n" +
		"ready. The note is required and is preserved in the audit trail — the person\n" +
		"adopting the draft reads your account before they read the draft, and an auditor\n" +
		"reading the filed workpaper months later can still see what you claimed at the\n" +
		"time.\n\n" +
		"Say what you did, and say what you could NOT verify. The second half is the part\n" +
		"a human has to pick up.",
	Args: exactArgs(1),
	RunE: runIssueHandover,
}

func runIssueHandover(cmd *cobra.Command, args []string) error {
	note, _ := cmd.Flags().GetString("note")
	note = strings.TrimSpace(note)
	if note == "" {
		// A handover with no account is a status change wearing a better name,
		// and the field would be empty in exactly the runs that went badly.
		return errors.New("--note is required: say what you did and what you could not verify. " +
			"The person adopting this draft reads it before they read the draft")
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve issue: %w", err)
	}

	body := map[string]any{
		"status": auditmode.StatusAgentDelivered,
		// The same field a reviewer's note travels on. One field, one meaning:
		// the free text that accompanies a decision about a workpaper.
		"review_note": note,
	}
	var result map[string]any
	if err := client.PutJSON(ctx, "/api/issues/"+issueRef.ID, body, &result); err != nil {
		return fmt.Errorf("%w\n\n%s", err, handoverHint)
	}

	fmt.Fprintf(os.Stderr,
		"Workpaper %s handed over. It is now waiting for a person to adopt the draft.\n",
		issueDisplayKey(result))

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	return nil
}

// handoverHint turns the server's refusal into the three things that are
// actually wrong when a handover fails, so an agent stops instead of retrying.
const handoverHint = "A handover is only possible when all of these hold:\n" +
	"  - the issue belongs to an audit engagement (a project in an auditee workspace);\n" +
	"  - it is currently in 编制中 (drafting) — not already handed over, in review, or filed;\n" +
	"  - you are acting as an agent. A person adopts a draft, they do not hand one over."
