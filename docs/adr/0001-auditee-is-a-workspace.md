# An auditee is a workspace; there is no project-level access control

Audit confidentiality is enforced entirely by platform workspace membership: one auditee is one workspace, and one engagement is a project inside it. We considered making an engagement the isolation boundary instead, which is what the original design assumed, but `project` has no membership table and no ACL of any kind — only a single `lead_id` pointer. Building engagement-level isolation would mean adding an access check to every path that lists issues (lists, saved views, search, inbox, boards, agent dispatch, activity, attachment download) and keeping it correct there forever; missing one is a silent privilege leak.

Choosing the workspace boundary makes that entire cross-cutting surface disappear, and it matches how internal auditors actually think about secrecy — the question is "may you look at subsidiary A's books", not "may you look at subsidiary A's 2023 audit". Cross-year comparison within one auditee is something audit needs to do, and a per-engagement wall would cut it off.

## Consequences

- `confidentiality_level` is a display label only. It filters nothing. Anything that needs to *enforce* separation must become a separate workspace.
- Workspace-scoped configuration (status catalog, custom properties, skills) is per-auditee, so onboarding a new auditee means seeding it. Cross-auditee risk roll-up has no native path and must be built deliberately.
- If two audit teams inside the same auditee ever need to be kept apart from each other, this decision does not cover it and engagement-level ACL becomes a separate, separately-scheduled security project.
