-- Phase 3: Nomad, backups, notification channels, richer alerts, Git deploys.
create table nomad_clusters (
  id          uuid primary key default gen_random_uuid(),
  project_id  uuid not null references projects on delete cascade,
  key         text not null, -- region + sorted server peers
  region      text not null,
  datacenter  text not null,
  leader      text not null default '',
  peers       jsonb not null default '[]',
  nodes       jsonb not null default '[]',
  jobs        jsonb not null default '[]',
  allocations jsonb not null default '[]',
  deployments jsonb not null default '[]',
  reporters   uuid[] not null default '{}',
  updated_at  timestamptz not null default now(),
  unique (project_id, key)
);

alter table applications
  add column orchestrator text not null default 'docker' check (orchestrator in ('docker','nomad')),
  add column nomad_cluster_id uuid references nomad_clusters on delete set null,
  add column instances int not null default 1 check (instances between 1 and 100),
  add column repo_url text not null default '',
  add column branch text not null default '',
  add column dockerfile text not null default '',
  add column build_context text not null default '',
  add column repo_token_encrypted bytea,
  add column webhook_secret text not null default '',
  add column auto_deploy boolean not null default false;

alter table deployments
  add column commit_sha text not null default '',
  add column commit_message text not null default '',
  add column commit_author text not null default '',
  add column branch text not null default '';

create table backup_configs (
  database_id    uuid primary key references databases on delete cascade,
  interval_hours int not null default 24 check (interval_hours between 1 and 720),
  retention      int not null default 7 check (retention between 1 and 365),
  enabled        boolean not null default true,
  s3_encrypted   bytea, -- S3Target JSON
  last_run_at    timestamptz
);

create table backups (
  id           uuid primary key, -- = operation id
  database_id  uuid not null references databases on delete cascade,
  file         text not null,
  size         bigint not null default 0,
  location     text not null default '',
  status       text not null default 'running' check (status in ('running','succeeded','failed')),
  scheduled    boolean not null default false,
  created_at   timestamptz not null default now(),
  finished_at  timestamptz
);

create table notification_channels (
  id               uuid primary key default gen_random_uuid(),
  project_id       uuid not null references projects on delete cascade,
  name             text not null,
  type             text not null check (type in ('email','slack','discord','webhook')),
  config_encrypted bytea not null, -- {"url": ...} or {"to": [...]}
  created_at       timestamptz not null default now()
);

alter table alert_rules drop constraint alert_rules_metric_check;
alter table alert_rules add constraint alert_rules_metric_check check (metric in
  ('cpu','memory','disk','offline','io','replication_lag','db_connections','db_down','error_rate','container_restarts','deployment_failed'));
alter table alert_rules alter column threshold drop not null;
alter table alert_rules add column channel_ids uuid[] not null default '{}';

drop index alert_incidents_rule_id_server_id_idx;
alter table alert_incidents alter column server_id drop not null;
alter table alert_incidents
  add column target_type text not null default 'server',
  add column target_id text,
  add column target_name text not null default '',
  add column acknowledged_by uuid references users on delete set null,
  add column acknowledged_at timestamptz;
update alert_incidents set target_id = server_id::text;
alter table alert_incidents alter column target_id set not null;
create unique index on alert_incidents (rule_id, target_id) where resolved_at is null;
