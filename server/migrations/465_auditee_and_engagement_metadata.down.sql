ALTER TABLE project DROP CONSTRAINT IF EXISTS project_audit_period_ordered;
ALTER TABLE project DROP COLUMN IF EXISTS audit_type;
ALTER TABLE project DROP COLUMN IF EXISTS audit_period_end;
ALTER TABLE project DROP COLUMN IF EXISTS audit_period_start;
ALTER TABLE workspace DROP COLUMN IF EXISTS confidentiality;
ALTER TABLE workspace DROP COLUMN IF EXISTS client_name;
