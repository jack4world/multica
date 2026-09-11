import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { WorkspaceLanding } from "./workspace-landing";

// Where a bare `/{slug}` goes. An auditee opens on the 审计台; everyone else on
// Issues; nobody is sent anywhere before the answer is in.

const auditMode = vi.hoisted(() => ({
  current: { data: undefined as { enabled: boolean } | undefined, isPending: true },
}));
const replace = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/audit", () => ({ useAuditMode: () => auditMode.current }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ audit: () => "/acme/audit", issues: () => "/acme/issues" }),
}));
vi.mock("../navigation", () => ({ useNavigation: () => ({ replace }) }));

beforeEach(() => {
  replace.mockReset();
  auditMode.current = { data: undefined, isPending: true };
});

describe("WorkspaceLanding", () => {
  it("waits for the answer before going anywhere", () => {
    render(<WorkspaceLanding />);
    expect(replace).not.toHaveBeenCalled();
  });

  it("opens an auditee workspace on the audit desk", () => {
    auditMode.current = { data: { enabled: true }, isPending: false };
    render(<WorkspaceLanding />);
    expect(replace).toHaveBeenCalledWith("/acme/audit");
  });

  it("opens every other workspace on Issues, including when the question cannot be answered", () => {
    auditMode.current = { data: { enabled: false }, isPending: false };
    const { unmount } = render(<WorkspaceLanding />);
    expect(replace).toHaveBeenCalledWith("/acme/issues");
    unmount();

    replace.mockReset();
    // An older backend has no audit mode endpoint: the query settles with no
    // data, and that is an ordinary workspace, not a stuck page.
    auditMode.current = { data: undefined, isPending: false };
    render(<WorkspaceLanding />);
    expect(replace).toHaveBeenCalledWith("/acme/issues");
  });
});
