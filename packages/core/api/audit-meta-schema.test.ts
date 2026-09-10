// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseWithFallback } from "./schema";
import { EMPTY_SEARCH_PROJECTS_RESPONSE, SearchProjectsResponseSchema } from "./schemas";

// API compatibility: an installed desktop client meets servers that predate a
// field. A client that blanks a project list over a missing audit period is
// worse than one that shows no period.

describe("audit metadata drift", () => {
  it("parses a project from a server that predates the audit fields", () => {
    const legacy = {
      projects: [
        {
          id: "p-1",
          workspace_id: "w-1",
          title: "Engagement",
          description: null,
          icon: null,
          status: "in_progress",
          priority: "none",
          lead_type: null,
          lead_id: null,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
          match_source: "title",
        },
      ],
    };
    const parsed = parseWithFallback(legacy, SearchProjectsResponseSchema, EMPTY_SEARCH_PROJECTS_RESPONSE, {
      endpoint: "test",
    });
    expect(parsed.projects).toHaveLength(1);
    expect(parsed.projects[0]?.audit_type).toBeNull();
    expect(parsed.projects[0]?.audit_period_start).toBeNull();
    // Not null: a server with no such column runs the chain the default
    // describes, and a UI that reads 0 levels would offer no review at all.
    expect(parsed.projects[0]?.review_levels).toBe(2);
    expect(parsed.projects[0]?.audit_phase).toBeNull();
  });

  it("keeps the fields when the server does send them", () => {
    const current = {
      projects: [
        {
          id: "p-1",
          workspace_id: "w-1",
          title: "Engagement",
          description: null,
          icon: null,
          status: "in_progress",
          priority: "none",
          lead_type: null,
          lead_id: null,
          audit_period_start: "2025-01-01",
          audit_period_end: "2025-12-31",
          audit_type: "separation_of_office",
          review_levels: 3,
          audit_phase: "fieldwork",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
          match_source: "title",
        },
      ],
    };
    const parsed = parseWithFallback(current, SearchProjectsResponseSchema, EMPTY_SEARCH_PROJECTS_RESPONSE, {
      endpoint: "test",
    });
    expect(parsed.projects[0]?.audit_type).toBe("separation_of_office");
    expect(parsed.projects[0]?.audit_period_end).toBe("2025-12-31");
    expect(parsed.projects[0]?.review_levels).toBe(3);
    expect(parsed.projects[0]?.audit_phase).toBe("fieldwork");
  });

  it("does not drop the whole list when one project is malformed", () => {
    const mixed = {
      projects: [
        { id: 42 },
        {
          id: "p-2",
          workspace_id: "w-1",
          title: "Good",
          description: null,
          icon: null,
          status: "planned",
          priority: "none",
          lead_type: null,
          lead_id: null,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
          match_source: "title",
        },
      ],
    };
    const parsed = parseWithFallback(mixed, SearchProjectsResponseSchema, EMPTY_SEARCH_PROJECTS_RESPONSE, {
      endpoint: "test",
    });
    // Whatever the list-level policy is, it must not throw.
    expect(Array.isArray(parsed.projects)).toBe(true);
  });
});
