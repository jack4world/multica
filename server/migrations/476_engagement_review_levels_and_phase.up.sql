-- How many review levels this engagement runs, and which phase it is in.
--
-- TWO BY DEFAULT. 三级复核 is a CPA firm's quality-control rule; a company's
-- internal audit function typically runs 主审复核 → 部门负责人审定. A department
-- with two levels that is forced to invent a third reviewer produces a
-- signature that satisfies the software and nobody else, and teaches everyone
-- involved that the chain is theatre.
--
-- ON THE ENGAGEMENT, not the auditee: different engagements at the same client
-- legitimately run differently — a routine 内控审计 at two levels and a
-- 离任审计 at three. On the auditee, the strictest engagement's depth would be
-- forced onto every other one.
--
-- NOT NULL with a default rather than nullable: every engagement has a depth,
-- and an ordinary project simply never consults it. A null would mean "ask
-- somewhere else", and there is nowhere else.
ALTER TABLE project ADD COLUMN review_levels INT NOT NULL DEFAULT 2
    CHECK (review_levels BETWEEN 1 AND 3);

-- 立项/准备 → 现场实施 → 报告 → 后续跟踪. A fixed list, because being able to
-- count engagements by phase is the reason to record the phase. Nullable and
-- absent on an ordinary project, like the rest of the engagement metadata.
ALTER TABLE project ADD COLUMN audit_phase TEXT
    CHECK (audit_phase IS NULL OR audit_phase IN ('preparation', 'fieldwork', 'reporting', 'follow_up'));
