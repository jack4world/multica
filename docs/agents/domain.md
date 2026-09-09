# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root, or
- **`CONTEXT-MAP.md`** at the repo root if it exists: it points at one `CONTEXT.md` per context. Read each one relevant to the topic.
- **`docs/adr/`**: read ADRs that touch the area you're about to work in. In multi-context repos, also check `src/<context>/docs/adr/` for context-scoped decisions.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

## File structure

This repo is **multi-context**. `CONTEXT-MAP.md` at the root is the entry point:

```
/
├── CONTEXT-MAP.md                     ← lists the contexts and how they relate
├── docs/adr/                          ← system-wide decisions
│   ├── 0001-auditee-is-a-workspace.md
│   └── 0002-audit-mode-overrides-platform-product-nouns.md
└── docs/audit/CONTEXT.md              ← the Audit vertical's glossary
```

One context is deliberately unusual: **Platform** has no `CONTEXT.md`. Its vocabulary
contract is `apps/docs/content/docs/developers/conventions.mdx`, which CLAUDE.md
already names as the single source of truth for naming, the i18n glossary and
Chinese product voice. Read that file where the map points at it, and do not
create a second platform glossary beside it.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0007 (event-sourced orders), but worth reopening because…_
