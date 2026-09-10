package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Migration 475 removes a status. The interesting part is not the removal — it
// is that nothing may be left standing on it.

const reshapeMigration = "475_audit_reshape_drop_handover_and_label"

func reshapeSQL(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("../../migrations/" + reshapeMigration + ".up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return string(body)
}

// A workpaper sitting in 待采纳 when the status is removed would be stranded on
// a status nothing produces and no picker offers — worse than the status was.
// The move has to come BEFORE the delete, and in the same file.
func TestTheReshapeMovesWorkpapersBeforeRemovingTheirStatus(t *testing.T) {
	sql := reshapeSQL(t)
	move := strings.Index(sql, "UPDATE issue SET status = 'drafting'")
	drop := strings.Index(sql, "DELETE FROM issue_status")
	if move < 0 {
		t.Fatal("the migration does not move workpapers off the removed status")
	}
	if drop < 0 {
		t.Fatal("the migration does not remove the status")
	}
	if move > drop {
		t.Error("the status is removed before the workpapers are moved off it; " +
			"they would be left on a status nothing produces")
	}
}

// Only the seeded copies. A workspace that hand-made a status with the same key
// for its own reasons owns it, and a reshape of the audit vertical has no
// business deleting it.
func TestTheReshapeOnlyRemovesTheSeededStatus(t *testing.T) {
	sql := reshapeSQL(t)
	idx := strings.Index(sql, "DELETE FROM issue_status")
	stmt := sql[idx:]
	if end := strings.Index(stmt, ";"); end > 0 {
		stmt = stmt[:end]
	}
	if !strings.Contains(stmt, "is_system = FALSE") {
		t.Error("the delete does not exclude built-ins")
	}
	if !strings.Contains(stmt, "agent_delivered") {
		t.Error("the delete is not scoped to the removed key")
	}
}

// The down direction deliberately does NOT recreate the status: it is seeded
// per workspace by the application, so a rollback that inserted one here would
// create a row the seeder does not know about.
func TestTheReshapeRollbackDoesNotResurrectTheStatus(t *testing.T) {
	body, err := os.ReadFile("../../migrations/" + reshapeMigration + ".down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	if strings.Contains(string(body), "INSERT INTO issue_status") {
		t.Error("the rollback recreates a seeded status, which the application seeder does not know about")
	}
}

// Guard against the migration being registered as needing a concurrent-index
// cleanup it does not have: it builds no index.
func TestTheReshapeBuildsNoIndex(t *testing.T) {
	if _, ok := concurrentIndexCleanups[reshapeMigration]; ok {
		t.Error("registered for index cleanup but it builds no index")
	}
	_ = context.Background()
}
