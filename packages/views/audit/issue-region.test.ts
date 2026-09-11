// @vitest-environment node
import { describe, expect, it } from "vitest";
import { auditIssueRegion } from "./issue-region";

// Canonical test for the noun region. Call sites pick a string by the region
// and do not re-test this matrix.
describe("auditIssueRegion", () => {
  it("is nothing outside audit mode, whatever the project", () => {
    expect(auditIssueRegion(false, "p-1")).toBeNull();
    expect(auditIssueRegion(false, null)).toBeNull();
  });

  it("calls an issue inside an engagement a workpaper and one outside a remediation item", () => {
    expect(auditIssueRegion(true, "p-1")).toBe("workpaper");
    expect(auditIssueRegion(true, null)).toBe("remediation");
    expect(auditIssueRegion(true, undefined)).toBe("remediation");
    expect(auditIssueRegion(true, "")).toBe("remediation");
  });
});
