// @vitest-environment node
import { describe, expect, it } from "vitest";
import zh from "../locales/zh-Hans/issues.json";
import { reviewLevelLabel } from "./level-label";

// Canonical test for naming a review level. The queue page and the issue
// trail both call this; they do not re-test the matrix.

const t = ((accessor: (dict: unknown) => string) => accessor(zh)) as Parameters<typeof reviewLevelLabel>[0];

describe("reviewLevelLabel", () => {
  it("names each rank the way an auditor says it", () => {
    expect(reviewLevelLabel(t, "reviewer_l1")).toBe("一级复核（主审）");
    expect(reviewLevelLabel(t, "reviewer_l2")).toBe("二级复核（项目经理）");
    expect(reviewLevelLabel(t, "reviewer_l3")).toBe("三级复核（部门负责人）");
  });

  it("never echoes a role code, even one it does not know", () => {
    for (const code of ["reviewer_l9", "", null, undefined]) {
      const label = reviewLevelLabel(t, code);
      expect(label).toBe("复核人");
      expect(label).not.toMatch(/reviewer_/);
    }
  });
});
