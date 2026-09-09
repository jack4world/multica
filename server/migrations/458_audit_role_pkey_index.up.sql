-- Backing index for audit_role's primary key, attached in 459 via
-- PRIMARY KEY USING INDEX. Own single-statement migration so CONCURRENTLY runs
-- outside an implicit transaction (repo convention).
CREATE UNIQUE INDEX CONCURRENTLY audit_role_pkey_uidx
    ON audit_role (id);
