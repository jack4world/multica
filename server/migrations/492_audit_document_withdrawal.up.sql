-- Withdrawing a document instead of deleting it.
--
-- 审计资料 IS the evidence — the auditee's vouchers, contracts, bank
-- statements — and removing one used to be a hard DELETE that any workspace
-- member could perform, writing nothing anywhere. That is the one hole an
-- append-only trail cannot cover: what was never recorded needs no altering.
-- A document removed before archival left a complete-looking 卷宗, a matching
-- sha256 and a clean trail, with nothing to say it had ever existed.
--
-- Audit practice is 撤下并注明原因, not disappearance. "This was here, and was
-- withdrawn by X at T because R" is honest; absence is not.
ALTER TABLE audit_document ADD COLUMN withdrawn_at TIMESTAMPTZ;
ALTER TABLE audit_document ADD COLUMN withdrawn_by UUID;
ALTER TABLE audit_document ADD COLUMN withdrawal_reason TEXT;
