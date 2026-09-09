# Context Map

## Contexts

- **Platform** — the general-purpose Human+Agent collaboration product (workspaces, issues, agents, runs, skills). Its vocabulary contract lives in [`apps/docs/content/docs/developers/conventions.mdx`](./apps/docs/content/docs/developers/conventions.mdx), not in a `CONTEXT.md`.
- [**Audit**](./docs/audit/CONTEXT.md) — the internal-audit vertical: engagements, workpapers, three-level review, observations.

## Relationships

- **Audit → Platform**: Audit does not own entities of its own where the Platform already has one. An auditee IS a workspace, an engagement IS a project, a workpaper IS an issue, a remediation item IS an issue. Audit adds tables only for concepts the Platform has no equivalent of.
- **Audit → Platform (vocabulary)**: Audit mode overrides the Platform's Chinese product nouns at the i18n layer only. Code identifiers never change. See ADR-0002.
- **Audit → Platform (isolation)**: Audit relies entirely on the Platform's workspace membership for access isolation and adds no access-control layer of its own. See ADR-0001.
