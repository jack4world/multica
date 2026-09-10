import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { AuditReport } from "@multica/core/types";
import zh from "../locales/zh-Hans/issues.json";
import { AuditReportPage } from "./audit-report-page";

// What a 主审 and a 部门负责人 see of the report. The chain and the rendering
// are canonical on the server and exercised there; this covers the wiring, the
// copy, and the two things a reader must be able to SEE — that a draft is a
// draft, and that an issued report can no longer be edited.

const reports = vi.fn<() => AuditReport[]>(() => []);
const update = vi.fn();
const create = vi.fn();
const archive = vi.fn();
const project = vi.fn<() => { audit_archived_at?: string | null }>(() => ({}));

function report(over: Partial<AuditReport> = {}): AuditReport {
  return {
    id: "r-1",
    project_id: "p-1",
    version: 1,
    status: "drafting",
    title: "2025 年度审计报告",
    background: "对采购循环进行了检查。",
    basis: "",
    scope: "",
    opinion: "",
    requirements: "",
    findings: [],
    workpaper_count: 12,
    filed_workpaper_count: 12,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

vi.mock("@multica/core/audit", () => ({
  useAuditReports: () => ({ data: reports(), isPending: false }),
  useCreateAuditReport: () => ({ mutate: create, isPending: false }),
  useUpdateAuditReport: () => ({ mutate: update, isPending: false }),
  useArchiveEngagement: () => ({ mutate: archive, isPending: false }),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/projects", () => ({ projectDetailOptions: () => ({ queryKey: ["project"] }) }));
vi.mock("@tanstack/react-query", () => ({ useQuery: () => ({ data: project() }) }));
vi.mock("@multica/core/api", () => ({ api: { exportAuditReport: vi.fn() } }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));
vi.mock("../i18n", () => ({
  useT: () => ({ t: (accessor: (dict: unknown) => string) => accessor(zh) }),
}));

beforeEach(() => {
  reports.mockReturnValue([]);
  project.mockReturnValue({});
  update.mockReset();
  create.mockReset();
  archive.mockReset();
});

describe("audit report", () => {
  it("offers to start one when the engagement has none", () => {
    render(<AuditReportPage projectId="p-1" />);
    expect(screen.getByText("本审计项目还没有报告。")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "起草报告" }));
    expect(create).toHaveBeenCalledWith(
      expect.objectContaining({ projectId: "p-1" }),
      expect.anything(),
    );
  });

  it("says a draft is a draft, because the failure mode is that someone sends it", () => {
    reports.mockReturnValue([report()]);
    render(<AuditReportPage projectId="p-1" />);
    expect(screen.getByText("草稿，未签发，不得对外。")).toBeInTheDocument();
  });

  it("stops offering edits once the report is issued", () => {
    reports.mockReturnValue([
      report({ status: "issued", issued_at: "2026-03-01T08:00:00Z" }),
    ]);
    render(<AuditReportPage projectId="p-1" />);
    expect(screen.queryByLabelText("一、基本情况")).not.toBeInTheDocument();
    expect(screen.getByText("对采购循环进行了检查。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "签发" })).not.toBeInTheDocument();
  });

  it("commits a section when the writer leaves it, not on every keystroke", async () => {
    reports.mockReturnValue([report()]);
    render(<AuditReportPage projectId="p-1" />);
    const opinion = screen.getByLabelText("五、审计意见");
    fireEvent.change(opinion, { target: { value: "总体有效。" } });
    expect(update).not.toHaveBeenCalled();

    fireEvent.blur(opinion);
    await waitFor(() => {
      expect(update).toHaveBeenCalledWith(
        expect.objectContaining({ reportId: "r-1", data: { opinion: "总体有效。" } }),
        expect.anything(),
      );
    });
  });

  it("offers sign-off only once the report has been submitted for it", () => {
    reports.mockReturnValue([report({ status: "drafting" })]);
    const { rerender } = render(<AuditReportPage projectId="p-1" />);
    expect(screen.getByRole("button", { name: "提交签发" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "签发" })).not.toBeInTheDocument();

    reports.mockReturnValue([report({ status: "reviewing" })]);
    rerender(<AuditReportPage projectId="p-1" />);
    expect(screen.getByRole("button", { name: "签发" })).toBeInTheDocument();
  });

  it("will not send a report back without a reason", () => {
    reports.mockReturnValue([report({ status: "reviewing" })]);
    render(<AuditReportPage projectId="p-1" />);
    fireEvent.click(screen.getByRole("button", { name: "退回起草人" }));
    expect(screen.getByRole("button", { name: "退回" })).toBeDisabled();
  });

  it("says whether the findings are live or frozen", () => {
    reports.mockReturnValue([report({ status: "drafting" })]);
    const { rerender } = render(<AuditReportPage projectId="p-1" />);
    expect(screen.getByText("草稿显示的是台账当前的样子；签发时会定格。")).toBeInTheDocument();

    reports.mockReturnValue([report({ status: "issued" })]);
    rerender(<AuditReportPage projectId="p-1" />);
    expect(
      screen.getByText("以下为签发时定格的内容，之后台账的变化不影响本报告。"),
    ).toBeInTheDocument();
  });

  it("records the work behind the report", () => {
    reports.mockReturnValue([report({ workpaper_count: 12, filed_workpaper_count: 11 })]);
    render(<AuditReportPage projectId="p-1" />);
    expect(screen.getByText("工作底稿共 12 份，已归档 11 份。")).toBeInTheDocument();
  });

  it("offers archival, and says so once the file is closed", () => {
    reports.mockReturnValue([report({ status: "issued" })]);
    render(<AuditReportPage projectId="p-1" />);
    fireEvent.click(screen.getByRole("button", { name: "归档本审计项目" }));
    expect(archive).toHaveBeenCalledWith("p-1", expect.anything());

    project.mockReturnValue({ audit_archived_at: "2026-03-02T00:00:00Z" });
    render(<AuditReportPage projectId="p-1" />);
    expect(screen.getByText("已于 2026-03-02 归档，卷宗已封存。")).toBeInTheDocument();
  });
});
