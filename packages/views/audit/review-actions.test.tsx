import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { AuditAction } from "@multica/core/types";
import zh from "../locales/zh-Hans/issues.json";
import { ReviewActions } from "./review-actions";

// What a reviewer sees and can do. The matrix behind these buttons is the
// canonical property of the server's gate package and is not replayed here;
// this covers the wiring, the copy, and the two rules a reader must be able to
// SEE rather than merely be refused by.

const actions = vi.fn<() => AuditAction[]>(() => []);
const mutate = vi.fn();
const isPending = { value: false };

vi.mock("@multica/core/audit", () => ({
  useAuditActions: () => ({ data: actions(), isPending: false }),
  useReviewAction: () => ({ mutate, isPending: isPending.value }),
}));

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));

// Chinese, because the point of half this work is that an auditor is not shown
// English machine identifiers.
vi.mock("../i18n", () => ({
  useT: () => ({
    t: (accessor: (dict: unknown) => string) => accessor(zh),
  }),
}));

beforeEach(() => {
  actions.mockReturnValue([]);
  mutate.mockReset();
  isPending.value = false;
});

describe("review actions", () => {
  it("renders nothing when the server offers nothing", () => {
    // A filed workpaper, someone else's level, one's own work. The absence of
    // buttons is the rule showing through, not an empty state to decorate.
    const { container } = render(<ReviewActions wsId="ws-1" issueId="i-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the pass in the reader's language, not the status key", () => {
    actions.mockReturnValue([
      { event: "workpaper_review_passed", to: "review_l2", requires_reason: false },
    ]);
    render(<ReviewActions wsId="ws-1" issueId="i-1" />);
    expect(screen.getByRole("button", { name: "通过，送下一级" })).toBeInTheDocument();
    expect(screen.queryByText("review_l2")).not.toBeInTheDocument();
  });

  it("names filing as filing, because it is the moment the workpaper locks", () => {
    actions.mockReturnValue([
      { event: "workpaper_filed", to: "filed", requires_reason: false },
    ]);
    render(<ReviewActions wsId="ws-1" issueId="i-1" />);
    expect(screen.getByRole("button", { name: "通过并归档" })).toBeInTheDocument();
  });

  it("sends the status the server named, not one it computed", () => {
    actions.mockReturnValue([
      { event: "workpaper_review_passed", to: "review_l3", requires_reason: false },
    ]);
    render(<ReviewActions wsId="ws-1" issueId="i-1" />);
    fireEvent.click(screen.getByRole("button", { name: "通过，送下一级" }));
    expect(mutate).toHaveBeenCalledWith(
      expect.objectContaining({ issueId: "i-1", to: "review_l3" }),
      expect.anything(),
    );
  });

  it("will not send a return until a reason is typed", async () => {
    // The requirement is enforced on the server too. Blocking it here is what
    // stops the reviewer discovering it as a refusal.
    actions.mockReturnValue([
      { event: "workpaper_review_rejected", to: "drafting", requires_reason: true },
    ]);
    render(<ReviewActions wsId="ws-1" issueId="i-1" />);
    fireEvent.click(screen.getByRole("button", { name: "退回编制人" }));

    const confirm = await screen.findByRole("button", { name: "退回编制人" });
    expect(confirm).toBeDisabled();

    fireEvent.change(screen.getByLabelText("退回理由"), {
      target: { value: "   " },
    });
    expect(screen.getByRole("button", { name: "退回编制人" })).toBeDisabled();
  });

  it("sends the typed reason with the return", async () => {
    actions.mockReturnValue([
      { event: "workpaper_review_rejected", to: "drafting", requires_reason: true },
    ]);
    render(<ReviewActions wsId="ws-1" issueId="i-1" />);
    fireEvent.click(screen.getByRole("button", { name: "退回编制人" }));
    fireEvent.change(screen.getByLabelText("退回理由"), {
      target: { value: "抽样依据没写清楚" },
    });
    fireEvent.click(screen.getByRole("button", { name: "退回编制人" }));

    await waitFor(() => {
      expect(mutate).toHaveBeenCalledWith(
        expect.objectContaining({ to: "drafting", reason: "抽样依据没写清楚" }),
        expect.anything(),
      );
    });
  });
});
