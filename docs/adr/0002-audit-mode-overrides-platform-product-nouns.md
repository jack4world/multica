# Audit mode overrides the platform's product nouns, at the i18n layer only

`apps/docs/content/docs/developers/conventions.mdx` is the repo's vocabulary contract and states that nothing overrides it: `issue` is 任务, `workspace` is 工作区, `project` is 项目. Auditors do not call a workpaper a 任务 or an auditee a 工作区, and a product that does reads as unserious to them. Audit mode therefore renders these nouns as 工作底稿, 被审计单位 and 审计项目 — and, for the same reason, as Workpaper, Auditee and Engagement in English. The override is semantic, not merely a translation fix, so it applies to every locale the vertical ships in; ja/ko get no override because the vertical has no business in those markets.

The override lives in the i18n layer and nowhere else. Code identifiers, database columns, API fields and route segments stay `issue`, `workspace` and `project` — the audit vertical adds no aliases, no wrapper types and no renamed columns. The whole cost of the override is a locale namespace.

## Consequences

- `conventions.mdx` carries an "audit mode vocabulary override" section listing every overridden noun. Without it the contract and the shipped copy disagree, and the next person to follow the contract will correctly "fix" the audit copy back to 任务.
- `issue` cannot be overridden globally: inside an engagement an issue is a workpaper, outside one it is a remediation item. The override is scoped per UI region, which is only workable because engagement membership is what distinguishes the two (see `docs/audit/CONTEXT.md`).
- Custom issue statuses render their editable `name`, not their key, so the review chain reads 一级复核 in Chinese without breaking the rule that the seven built-in status keys stay lowercase English.
- Seeded catalog names are stored rows, not i18n strings. A workspace is seeded in one locale at enable time and its names are editable from then on; changing the override table later does not restate them.
