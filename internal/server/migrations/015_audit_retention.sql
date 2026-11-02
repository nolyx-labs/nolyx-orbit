-- How long the audit log is kept, per organization. 0 keeps it forever (the default, so upgrading deletes nothing).
alter table organizations add column audit_retention_days int not null default 0
  check (audit_retention_days in (0, 90, 180, 365, 730));
