-- Server-wide backups: every Orbit service's data on a server, encrypted and stored in S3 or on another server.
create table server_backup_configs (
  server_id        uuid primary key references servers on delete cascade,
  schedule         text not null default 'manual' check (schedule in ('daily','weekly','manual')),
  destination      text not null check (destination in ('s3','server')),
  s3_provider      text not null default '',
  s3_encrypted     bytea,                                    -- protocol.S3Target
  target_server_id uuid references servers on delete set null,
  target_addr      text not null default '',                 -- host:port of the target's SSH server
  target_host_key  text not null default '',                 -- pinned when the destination was saved
  key_encrypted    bytea not null,                           -- archive key; never changes, old archives need it
  last_run_at      timestamptz,                              -- last scheduled run
  updated_at       timestamptz not null default now()
);

-- One row per service archive. Runs themselves are operations (server.backup, server.restore, server.snapshot).
create table server_backups (
  id           uuid primary key default gen_random_uuid(),
  server_id    uuid not null references servers on delete cascade,
  operation_id uuid references operations on delete set null,
  service      text not null, -- app/<name> or db/<name>
  kind         text not null check (kind in ('scheduled','manual','pre-deploy','pre-restore')),
  status       text not null check (status in ('succeeded','failed')),
  file         text not null default '',
  location     text not null default '',
  size         bigint not null default 0,
  duration_ms  bigint not null default 0,
  error        text not null default '',
  created_at   timestamptz not null default now()
);
create index on server_backups (server_id, created_at desc);

-- One backup run per server and one restore per service at a time.
create unique index operations_one_server_backup on operations (server_id) where kind = 'server.backup' and status = 'running';
create unique index operations_one_service_restore on operations (server_id, target) where kind = 'server.restore' and status = 'running';
create index on operations (server_id, kind, created_at desc);

alter table alert_rules drop constraint alert_rules_metric_check;
alter table alert_rules add constraint alert_rules_metric_check check (metric in
  ('cpu','memory','disk','offline','io','replication_lag','db_connections','db_down','error_rate','container_restarts',
   'deployment_failed','monitor_down','backup_failed'));
