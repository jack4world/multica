import type { useT } from "../i18n";

type IssuesT = ReturnType<typeof useT<"issues">>["t"];

/**
 * A review level the way an auditor says it.
 *
 * `reviewer_l1` is a role code. It belongs in the trail's `details` and in the
 * gate's decisions; it does not belong in front of a reader, where the same
 * rule refusal-copy.ts states applies: a Chinese-locale auditor should never
 * be shown `reviewer_l1`. Every surface that names a level — the queue, the
 * trail, a refusal — goes through here so they cannot disagree.
 *
 * An unknown code is named, not echoed: the fallback says "复核人" rather
 * than leaking whatever the server sent.
 */
export function reviewLevelLabel(t: IssuesT, level: string | null | undefined): string {
  switch (level) {
    case "reviewer_l1": return t(($) => $.audit.level.reviewer_l1);
    case "reviewer_l2": return t(($) => $.audit.level.reviewer_l2);
    case "reviewer_l3": return t(($) => $.audit.level.reviewer_l3);
    default: return t(($) => $.audit.level.fallback);
  }
}
