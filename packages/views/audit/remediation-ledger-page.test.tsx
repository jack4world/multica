import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { AuditDepartment, RemediationItem } from "@multica/core/types";
import zh from "../locales/zh-Hans/issues.json";
import { NavigationProvider } from "../navigation/context";
import type { NavigationAdapter } from "../navigation/types";
import { RemediationLedgerPage } from "./remediation-ledger-page";

// What a department head sees on the ledger. The rules behind it are the
// server's and are exercised there; this covers the wiring, the copy, and the
// three things a reader must be able to SEE.

const ledgerItems = vi.fn<() => RemediationItem[]>(() => []);
const departments = vi.fn<() => AuditDepartment[]>(() => []);
const ledgerFilters = vi.fn<(f: unknown) => void>();
const reassign = vi.fn();
const remove = vi.fn();
const create = vi.fn();

function item(over: Partial<RemediationItem> = {}): RemediationItem {
  return {
    issue_id: "i-1",
    title: "三份采购合同未审批",
    status: "remediating",
    due_date: "2026-01-31",
    overdue: false,
    department_id: "d-1",
    department_name: "采购部",
    source_project_id: "p-1",
    source_project_title: "2025 年离任审计",
    created_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

vi.mock("@multica/core/audit", () => ({
  useAuditMode: () => ({ data: { enabled: true, enabled_at: "2026-01-01T00:00:00Z" } }),
  useAuditDepartments: () => ({ data: departments() }),
  useRemediationLedger: (_ws: string, filters: unknown) => {
    ledgerFilters(filters);
    return { data: ledgerItems(), isPending: false };
  },
  useReassignRemediation: () => ({ mutate: reassign, isPending: false }),
  useCreateAuditDepartment: () => ({ mutate: create, isPending: false }),
  useDeleteAuditDepartment: () => ({ mutate: remove, isPending: false }),
  useRaiseRemediation: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

// The catalog is the workspace's own, and its names are editable. The ledger
// must render what the auditee called the status, never the stored key.
vi.mock("@multica/core/issue-statuses", () => ({
  useIssueStatuses: () => ({
    labelOf: (key: string) => (key === "remediating" ? "整改中" : key),
  }),
}));

vi.mock("@multica/core/paths", () => ({
  paths: { workspace: () => ({ issueDetail: (id: string) => `/acme/issues/${id}` }) },
  useWorkspaceSlug: () => "acme",
}));

vi.mock("@multica/core/projects", () => ({ projectListOptions: () => ({ queryKey: ["projects"] }) }));

vi.mock("@tanstack/react-query", () => ({ useQuery: () => ({ data: [] }) }));

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));

vi.mock("../i18n", () => ({
  useT: () => ({ t: (accessor: (dict: unknown) => string) => accessor(zh) }),
}));

beforeEach(() => {
  ledgerItems.mockReturnValue([]);
  departments.mockReturnValue([]);
  ledgerFilters.mockReset();
  reassign.mockReset();
  remove.mockReset();
});

// AppLink resolves its href through the navigation adapter, the way it does in
// the real shell. Mocking the link instead would test a different component
// than the one that ships.
const navigation: NavigationAdapter = {
  push: vi.fn(),
  replace: vi.fn(),
  back: vi.fn(),
  pathname: "/acme/remediation",
  searchParams: new URLSearchParams(),
  hash: "",
  getShareableUrl: (path: string) => path,
};

function renderLedger() {
  return render(
    <NavigationProvider value={navigation}>
      <RemediationLedgerPage />
    </NavigationProvider>,
  );
}

describe("remediation ledger", () => {
  it("shows lateness the way the server reported it", () => {
    // NOT recomputed here. A browser's clock, its timezone and a stale cache
    // are three ways to disagree with the ledger about who owes what.
    ledgerItems.mockReturnValue([item({ overdue: true, days_late: 12 })]);
    renderLedger();
    expect(screen.getByText("逾期 12 天")).toBeInTheDocument();
  });

  it("does not mark an item late when the server did not", () => {
    ledgerItems.mockReturnValue([item({ due_date: "2020-01-01", overdue: false })]);
    renderLedger();
    // The badge, not the filter button that also says 逾期.
    expect(screen.queryByText(/逾期 \d+ 天/)).not.toBeInTheDocument();
  });

  it("tells an empty ledger apart from an empty filter", () => {
    renderLedger();
    expect(screen.getByText("台账上没有整改事项。")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "已逾期" }));
    expect(screen.getByText("没有符合这些条件的整改事项。")).toBeInTheDocument();
  });

  it("asks the server for the overdue ones rather than filtering in the browser", async () => {
    renderLedger();
    fireEvent.click(screen.getByRole("button", { name: "已逾期" }));
    await waitFor(() => {
      expect(ledgerFilters).toHaveBeenLastCalledWith(expect.objectContaining({ overdue: true }));
    });
  });

  it("will not offer to remove a department that still owes items", () => {
    departments.mockReturnValue([
      { id: "d-1", name: "采购部", item_count: 3 },
      { id: "d-2", name: "临时部门", item_count: 0 },
    ]);
    renderLedger();
    const buttons = screen.getAllByRole("button", { name: "删除" });
    expect(buttons[0]).toBeDisabled();
    expect(buttons[1]).toBeEnabled();
  });

  it("shows what each department owes, which is the reason the list is closed", () => {
    departments.mockReturnValue([{ id: "d-1", name: "采购部", item_count: 3 }]);
    renderLedger();
    expect(screen.getByText("3 条事项")).toBeInTheDocument();
  });

  it("names the status the way the auditee named it, not by its key", () => {
    ledgerItems.mockReturnValue([item()]);
    renderLedger();
    expect(screen.getByText("整改中")).toBeInTheDocument();
    expect(screen.queryByText("remediating")).not.toBeInTheDocument();
  });

  it("links an item to the issue behind it", () => {
    ledgerItems.mockReturnValue([item()]);
    renderLedger();
    expect(screen.getByRole("link", { name: "三份采购合同未审批" })).toHaveAttribute(
      "href",
      "/acme/issues/i-1",
    );
  });

  it("says an item has no deadline rather than leaving the cell blank", () => {
    ledgerItems.mockReturnValue([item({ due_date: undefined })]);
    renderLedger();
    expect(screen.getByText("无期限")).toBeInTheDocument();
  });
});
