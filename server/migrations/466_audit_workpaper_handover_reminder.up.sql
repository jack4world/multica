-- When a delivered draft was last reminded about.
--
-- The reminder is sent ONCE, not daily. A forgotten draft that generates an
-- inbox item every morning trains people to ignore the inbox, which costs more
-- than the forgotten draft did. This column is what makes "once" true across
-- restarts and catch-up runs.
--
-- On audit_workpaper rather than on issue: it is an audit-only fact about a
-- workpaper, and issue is the hottest table in the schema.
--
-- No index. The reminder job reads workpapers by their issue's status, which is
-- the selective predicate; this column only filters the handful that come back.
ALTER TABLE audit_workpaper ADD COLUMN handover_reminded_at TIMESTAMPTZ;

-- preparer_id becomes nullable.
--
-- A workpaper an agent handed over may never have been SUBMITTED by a person,
-- so it can legitimately have a row with no preparer — the reminder needs a row
-- to record itself on, but there is no preparer yet to record. The alternative
-- was a sentinel "no member can be this" UUID, which is the kind of value that
-- reads as real everywhere it is not specially handled.
--
-- Nothing is loosened by this: every place that reads a preparer already treats
-- its absence as "never submitted", because that was already representable as
-- a missing row.
ALTER TABLE audit_workpaper ALTER COLUMN preparer_id DROP NOT NULL;
