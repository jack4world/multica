// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ReviewQueueItem } from "@multica/core/types";
import { auditIssueNavKey } from "./issue-nav";

// Canonical test for "which nav item does this issue belong to". The sidebar
// test covers the wiring (data-active on the right button), not this matrix.

const waiting = [
  { issue: { id: "w-1", identifier: "AUDI-3" }, level: "reviewer_l1", waiting_since: "2026-01-01" },
] as ReviewQueueItem[];

describe("auditIssueNavKey", () => {
  it("defers to the platform outside audit mode and off issue routes", () => {
    expect(auditIssueNavKey(false, "w-1", waiting, { project_id: "p-1" })).toBeNull();
    expect(auditIssueNavKey(true, null, waiting, { project_id: null })).toBeNull();
  });

  it("puts a workpaper waiting on the viewer under 待我复核, by id or identifier", () => {
    expect(auditIssueNavKey(true, "w-1", waiting, { project_id: "p-1" })).toBe("reviewQueue");
    expect(auditIssueNavKey(true, "AUDI-3", waiting, { project_id: "p-1" })).toBe("reviewQueue");
  });

  it("puts every remediation item under 整改台账, whatever its state", () => {
    expect(auditIssueNavKey(true, "r-1", waiting, { project_id: null })).toBe("remediation");
    expect(auditIssueNavKey(true, "r-1", [], { project_id: undefined })).toBe("remediation");
  });

  it("leaves a workpaper that is not waiting on the viewer to the platform rule", () => {
    expect(auditIssueNavKey(true, "w-2", waiting, { project_id: "p-1" })).toBeNull();
    // Not loaded yet: say nothing rather than guess.
    expect(auditIssueNavKey(true, "w-2", [], undefined)).toBeNull();
  });
});
