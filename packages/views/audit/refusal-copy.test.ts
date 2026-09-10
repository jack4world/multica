import { describe, expect, it } from "vitest";
import { ApiError } from "@multica/core/api";
import { AUDIT_REFUSAL_CODES, refusalCode, refusalFallback } from "./refusal-copy";

// The server sends a code so the client can translate the refusal. These cover
// the reading of it — the copy itself is asserted where it renders.

describe("refusal code", () => {
  it("reads the code out of the error envelope", () => {
    const err = new ApiError("this step needs reviewer_l1", 403, "Forbidden", {
      error: "this step needs reviewer_l1",
      code: "level_required",
    });
    expect(refusalCode(err)).toBe("level_required");
  });

  it("ignores a code it has no copy for, so the sentence is used instead", () => {
    // Degrading to something true beats degrading to an empty toast.
    const err = new ApiError("something new", 409, "Conflict", { code: "invented_later" });
    expect(refusalCode(err)).toBeUndefined();
    expect(refusalFallback(err)).toBe("something new");
  });

  it("never surfaces a 5xx message", () => {
    // Those carry Go error chains and pgx constraint names.
    const err = new ApiError("pq: duplicate key value violates ...", 500, "Server Error", {});
    expect(refusalFallback(err)).toBeUndefined();
  });

  it("ignores a transport failure", () => {
    expect(refusalCode(new Error("Failed to fetch"))).toBeUndefined();
    expect(refusalFallback(new Error("Failed to fetch"))).toBeUndefined();
  });

  it("lists every code the gate classifies with", () => {
    // Kept in step by hand; this pins the list so a server-side addition is a
    // visible diff here rather than a silently untranslated toast.
    expect([...AUDIT_REFUSAL_CODES].sort()).toEqual([
      "agent_not_permitted",
      "archive_not_configured",
      "engagement_archived",
      "filed",
      "illegal_transition",
      "leaves_chain",
      "level_required",
      "not_a_remediation_item",
      "not_responsible",
      "note_required",
      "rank_required",
      "reason_required",
      "remediation_closed",
      "report_issued",
      "report_required",
      "self_review",
      "self_verification",
      "verifier_required",
      "workpapers_unfinished",
    ]);
  });
});
