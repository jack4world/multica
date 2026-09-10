import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { AuditCategory, AuditDocument } from "@multica/core/types";
import zh from "../locales/zh-Hans/issues.json";
import { DocumentLibraryPage } from "./document-library-page";

// What an auditor sees of 审计资料. The filing scheme's rules — two levels,
// fixed-width segments, a drawer that still holds material cannot go — are the
// server's and are exercised there; this covers the wiring and the copy.

const categories = vi.fn<() => AuditCategory[]>(() => []);
const documents = vi.fn<() => AuditDocument[]>(() => []);
const requestedPath = vi.fn<(path: string) => void>();
const fileDocument = vi.fn();
const createCategory = vi.fn();
const removeDocument = vi.fn();

function category(over: Partial<AuditCategory> = {}): AuditCategory {
  return { path: "01", name: "内部控制制度", is_standard: true, depth: 1, ...over };
}

function document(over: Partial<AuditDocument> = {}): AuditDocument {
  return {
    id: "doc-1",
    category_path: "01",
    title: "采购管理办法",
    attachment_id: "a-1",
    filename: "purchasing.pdf",
    url: "https://example.test/purchasing.pdf",
    content_type: "application/pdf",
    size_bytes: 2 * 1024 * 1024,
    uploader_type: "member",
    uploader_id: "m-1",
    created_at: "2026-01-05T00:00:00Z",
    ...over,
  };
}

vi.mock("@multica/core/audit", () => ({
  useAuditMode: () => ({ data: { enabled: true, enabled_at: "2026-01-01T00:00:00Z" } }),
  useAuditCategories: () => ({ data: categories(), isPending: false }),
  useAuditDocuments: (_ws: string, path: string) => {
    requestedPath(path);
    return { data: documents(), isPending: false };
  },
  useCreateAuditCategory: () => ({ mutate: createCategory, isPending: false }),
  useDeleteAuditCategory: () => ({ mutate: vi.fn(), isPending: false }),
  useFileAuditDocument: () => ({ mutate: fileDocument, isPending: false }),
  useDeleteAuditDocument: () => ({ mutate: removeDocument, isPending: false }),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));
vi.mock("../i18n", () => ({
  useT: () => ({ t: (accessor: (dict: unknown) => string) => accessor(zh) }),
}));

beforeEach(() => {
  categories.mockReturnValue([]);
  documents.mockReturnValue([]);
  requestedPath.mockReset();
  fileDocument.mockReset();
  createCategory.mockReset();
  removeDocument.mockReset();
});

describe("document library", () => {
  it("opens on the first drawer rather than on nothing", () => {
    categories.mockReturnValue([category(), category({ path: "02", name: "会议纪要" })]);
    render(<DocumentLibraryPage />);
    expect(requestedPath).toHaveBeenCalledWith("01");
  });

  it("asks the server for the drawer the reader picked", async () => {
    categories.mockReturnValue([category(), category({ path: "02", name: "会议纪要" })]);
    render(<DocumentLibraryPage />);
    fireEvent.click(screen.getByRole("button", { name: /会议纪要/ }));
    await waitFor(() => {
      expect(requestedPath).toHaveBeenLastCalledWith("02");
    });
  });

  it("shows the classification number beside the name, because that is the filing", () => {
    categories.mockReturnValue([category({ path: "03", name: "账务凭证" })]);
    render(<DocumentLibraryPage />);
    expect(screen.getByText("03")).toBeInTheDocument();
  });

  it("says an empty drawer is empty", () => {
    categories.mockReturnValue([category()]);
    render(<DocumentLibraryPage />);
    expect(screen.getByText("本类目下还没有资料。")).toBeInTheDocument();
  });

  it("renders sizes people can read", () => {
    categories.mockReturnValue([category()]);
    documents.mockReturnValue([document()]);
    render(<DocumentLibraryPage />);
    expect(screen.getByText("2.0 MB")).toBeInTheDocument();
  });

  it("files what was chosen into the drawer that is open", () => {
    categories.mockReturnValue([category({ path: "02", name: "会议纪要" })]);
    const { container } = render(<DocumentLibraryPage />);
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const chosen = new File(["x"], "minutes.pdf", { type: "application/pdf" });
    fireEvent.change(input, { target: { files: [chosen] } });

    expect(fileDocument).toHaveBeenCalledWith(
      expect.objectContaining({ categoryPath: "02", file: chosen }),
      expect.anything(),
    );
  });

  it("will not offer a third level, because the scheme is two deep", () => {
    categories.mockReturnValue([category({ path: "01/01", name: "采购制度", depth: 2 })]);
    render(<DocumentLibraryPage />);
    expect(screen.queryByText("归档号（两位数字，如 08）")).not.toBeInTheDocument();
  });

  it("builds a child's path under the drawer it is added to", () => {
    categories.mockReturnValue([category({ path: "07", name: "其他", is_standard: false })]);
    render(<DocumentLibraryPage />);
    fireEvent.change(screen.getByLabelText("归档号（两位数字，如 08）"), {
      target: { value: "01" },
    });
    fireEvent.change(screen.getByLabelText("类目名称"), { target: { value: "往来函证" } });
    fireEvent.click(screen.getByRole("button", { name: "新增" }));

    expect(createCategory).toHaveBeenCalledWith(
      { path: "07/01", name: "往来函证" },
      expect.anything(),
    );
  });

  it("will not add a drawer whose number is not two digits", () => {
    categories.mockReturnValue([category({ path: "07", name: "其他", is_standard: false })]);
    render(<DocumentLibraryPage />);
    fireEvent.change(screen.getByLabelText("归档号（两位数字，如 08）"), { target: { value: "7" } });
    fireEvent.change(screen.getByLabelText("类目名称"), { target: { value: "往来函证" } });
    expect(screen.getByRole("button", { name: "新增" })).toBeDisabled();
  });
});
