import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Project, RemediationItem, ReviewQueueItem } from "@multica/core/types";
import zh from "../locales/zh-Hans/issues.json";
import { AuditHomePage } from "./audit-home-page";

// What a first-time auditor sees on opening the workspace. The numbers are the
// server's; this covers that the page shows them, sends each button to the
// page the number came from, and puts a question in front of an agent.

const queue = vi.fn<() => ReviewQueueItem[]>(() => []);
const ledger = vi.fn<(filters: unknown) => RemediationItem[]>(() => []);
const projects = vi.fn<() => Project[]>(() => []);
const chat = vi.hoisted(() => ({
  setOpen: vi.fn(),
  setActiveSession: vi.fn(),
  setInputDraft: vi.fn(),
  floatingChatEnabled: true,
}));
const push = vi.hoisted(() => vi.fn());

function remediation(over: Partial<RemediationItem> = {}): RemediationItem {
  return {
    issue_id: "i-1",
    title: "三份采购合同未审批",
    status: "remediating",
    overdue: false,
    department_id: "d-1",
    department_name: "采购部",
    source_project_id: "p-1",
    source_project_title: "2025 年离任审计",
    created_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

function project(over: Partial<Project> = {}): Project {
  return {
    id: "p-1",
    title: "2025 年离任审计",
    audit_phase: "fieldwork",
    audit_archived_at: null,
    ...over,
  } as Project;
}

vi.mock("@multica/core/audit", () => ({
  useAuditMode: () => ({ data: { enabled: true, enabled_at: "2026-01-01T00:00:00Z" } }),
  useReviewQueue: () => ({ data: queue(), isPending: false }),
  useRemediationLedger: (_ws: string, filters: unknown) => ({
    data: ledger(filters),
    isPending: false,
  }),
}));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string; name: string } }) => unknown) =>
    selector({ user: { id: "user-1", name: "李审" } }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/paths", () => ({
  paths: {
    workspace: (slug: string) => ({
      reviewQueue: () => `/${slug}/review-queue`,
      remediation: () => `/${slug}/remediation`,
      auditDocuments: () => `/${slug}/audit-documents`,
      projectDetail: (id: string) => `/${slug}/projects/${id}`,
      projectReport: (id: string) => `/${slug}/projects/${id}/report`,
      chat: () => `/${slug}/chat`,
    }),
  },
  useWorkspaceSlug: () => "acme",
}));
vi.mock("@multica/core/projects/queries", () => ({
  projectListOptions: () => ({ queryKey: ["projects"] }),
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: projects(), isPending: false }),
}));
// Callable-store shape (selectorFn + getState) per the repo testing rules.
vi.mock("@multica/core/chat", () => ({
  DRAFT_NEW_SESSION: "__new__",
  useChatStore: Object.assign(
    (selector: (s: typeof chat) => unknown) => selector(chat),
    { getState: () => chat },
  ),
}));
vi.mock("../navigation", () => ({
  useNavigation: () => ({ push, pathname: "/acme/audit" }),
}));
vi.mock("../navigation/app-link", () => ({
  AppLink: ({ children, href, ...rest }: { children?: React.ReactNode; href: string }) => (
    <a href={href} {...rest}>{children}</a>
  ),
}));
vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuItem: ({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) => (
    <button type="button" onClick={onClick}>{children}</button>
  ),
  DropdownMenuSeparator: () => null,
  DropdownMenuTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
}));
vi.mock("../i18n", () => ({
  useT: () => ({
    t: (accessor: (dict: unknown) => string, vars?: Record<string, string | number>) => {
      // Interpolate the way i18next does — `{{name}}`, double braces — so a
      // string written with single braces fails here the way it fails in
      // the product (it rendered "{count} 份底稿等你" once).
      let text = accessor(zh);
      for (const [k, v] of Object.entries(vars ?? {})) text = text.replaceAll(`{{${k}}}`, String(v));
      return text;
    },
  }),
}));

beforeEach(() => {
  queue.mockReturnValue([]);
  ledger.mockReturnValue([]);
  projects.mockReturnValue([]);
  chat.setOpen.mockReset();
  chat.setActiveSession.mockReset();
  chat.setInputDraft.mockReset();
  chat.floatingChatEnabled = true;
  push.mockReset();
});

describe("AuditHomePage", () => {
  it("greets the auditor by name and says nothing is waiting", () => {
    render(<AuditHomePage />);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("李审，今天先看这些");
    expect(screen.getByText("没有等你复核的底稿")).toBeInTheDocument();
    expect(screen.getByText("没有你负责的整改事项")).toBeInTheDocument();
    expect(screen.getByText("没有逾期的整改")).toBeInTheDocument();
    expect(screen.getByText("还没有进行中的审计项目")).toBeInTheDocument();
  });

  it("counts what is waiting and sends each button to the page it came from", () => {
    queue.mockReturnValue([
      { issue: { id: "w-1", title: "底稿 A" }, level: "reviewer_l1", waiting_since: "2026-01-02" },
      { issue: { id: "w-2", title: "底稿 B" }, level: "reviewer_l1", waiting_since: "2026-01-03" },
    ] as ReviewQueueItem[]);
    // The ledger is asked twice: once for the viewer's own items, once for the
    // unit's overdue ones. Each answer must land on its own card.
    ledger.mockImplementation((filters) => {
      const f = filters as { assignee_id?: string; overdue?: boolean };
      if (f.assignee_id === "user-1") {
        return [
          remediation({ issue_id: "m-1", overdue: true, days_late: 3 }),
          remediation({ issue_id: "m-2" }),
          remediation({ issue_id: "m-3", status: "remediation_closed" }),
        ];
      }
      if (f.overdue === true) return [remediation({ issue_id: "o-1", overdue: true })];
      return [];
    });

    render(<AuditHomePage />);

    expect(screen.getByText("2 份底稿等你")).toBeInTheDocument();
    // Closed items are not "in progress"; the overdue one is called out.
    expect(screen.getByText("2 条在办")).toBeInTheDocument();
    expect(screen.getByText("其中 1 条已逾期")).toBeInTheDocument();
    expect(screen.getByText("1 条逾期")).toBeInTheDocument();

    expect(screen.getByRole("link", { name: "去复核" })).toHaveAttribute("href", "/acme/review-queue");
    expect(screen.getByRole("link", { name: "去处理" })).toHaveAttribute("href", "/acme/remediation");
    expect(screen.getByRole("link", { name: "看台账" })).toHaveAttribute("href", "/acme/remediation");
    expect(screen.getByRole("link", { name: "去资料库" })).toHaveAttribute("href", "/acme/audit-documents");
  });

  it("lists open engagements with their phase and a way to the report, and hides archived ones", () => {
    projects.mockReturnValue([
      project(),
      project({ id: "p-2", title: "2024 年经济责任审计", audit_archived_at: "2026-01-01T00:00:00Z" }),
    ]);
    render(<AuditHomePage />);
    expect(screen.getByRole("link", { name: "2025 年离任审计" })).toHaveAttribute("href", "/acme/projects/p-1");
    expect(screen.getByText("现场")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "出报告" })).toHaveAttribute("href", "/acme/projects/p-1/report");
    expect(screen.queryByText("2024 年经济责任审计")).not.toBeInTheDocument();
  });

  it("puts a starter question in a fresh chat and opens the floating window", () => {
    render(<AuditHomePage />);
    fireEvent.click(screen.getAllByText("我是新来的审计人员，请用大白话告诉我现在该做什么。")[0]!);
    expect(chat.setActiveSession).toHaveBeenCalledWith(null);
    expect(chat.setInputDraft).toHaveBeenCalledWith(
      "__new__",
      "我是新来的审计人员，请用大白话告诉我现在该做什么。",
    );
    expect(chat.setOpen).toHaveBeenCalledWith(true);
    expect(push).not.toHaveBeenCalled();
  });

  it("goes to the Chat page instead when the floating window is turned off", () => {
    chat.floatingChatEnabled = false;
    render(<AuditHomePage />);
    fireEvent.click(screen.getAllByText("复核一份底稿要怎么操作？通过和退回分别会发生什么？")[0]!);
    expect(chat.setInputDraft).toHaveBeenCalled();
    expect(chat.setOpen).not.toHaveBeenCalled();
    expect(push).toHaveBeenCalledWith("/acme/chat");
  });

  it("opens an empty composer without clobbering a draft when asked to ask something else", () => {
    render(<AuditHomePage />);
    fireEvent.click(screen.getByText("我想问别的…"));
    expect(chat.setInputDraft).not.toHaveBeenCalled();
    expect(chat.setOpen).toHaveBeenCalledWith(true);
  });
});
