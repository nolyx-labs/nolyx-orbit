-- Phase 2: PostgreSQL monitoring & replicas, central logs, request metrics, secrets, API keys.
create extension if not exists pg_trgm;

alter table servers add column nomad_version text not null default '';
alter table server_metrics
  add column io_read bigint not null default 0,
  add column io_write bigint not null default 0,
  add column io_util real not null default 0;
alter table applications
  add column proxy boolean not null default false,
  add column cpus real not null default 0,
  add column memory_mb int not null default 0;
alter table deployments add column plan jsonb not null default '[]';

create table databases (
  id                 uuid primary key default gen_random_uuid(),
  project_id         uuid not null references projects on delete cascade,
  server_id          uuid not null references servers on delete cascade, -- agent that monitors (and for managed: runs) it
  name               text not null,
  engine             text not null default 'postgres' check (engine in ('postgres')),
  host               text not null,
  port               int not null,
  username           text not null,
  dbname             text not null,
  sslmode            text not null default 'prefer',
  password_encrypted bytea not null,
  managed            boolean not null default false,
  version            text not null default '',
  repl_password_encrypted bytea,
  primary_id         uuid references databases on delete set null, -- intended upstream for managed replicas
  role               text not null default 'unknown' check (role in ('primary','replica','unknown')),
  status             text not null default 'unknown' check (status in ('healthy','degraded','down','unknown','provisioning','failed')),
  system_id          text not null default '',
  error              text not null default '',
  stats              jsonb not null default '{}',
  last_seen_at       timestamptz,
  created_at         timestamptz not null default now(),
  unique (project_id, name)
);

create table database_metrics (
  database_id     uuid not null references databases on delete cascade,
  ts              timestamptz not null,
  connections     int not null,
  max_connections int not null,
  tps             real,
  qps             real,
  cache_hit       real,
  size            bigint not null,
  waiting_locks   int not null,
  lag_ms          real,
  replay_rate     real,
  primary key (database_id, ts)
);

-- Long-running agent operations other than deployments (provisioning, backups, promotions, Nomad installs).
create table operations (
  id           uuid primary key,
  project_id   uuid not null references projects on delete cascade,
  server_id    uuid references servers on delete set null,
  database_id  uuid references databases on delete set null,
  kind         text not null,
  target       text not null,
  status       text not null default 'running' check (status in ('running','succeeded','failed')),
  plan         jsonb not null default '[]',
  steps        jsonb not null default '{}',
  log          text not null default '',
  error        text,
  output       jsonb,
  triggered_by uuid references users on delete set null,
  created_at   timestamptz not null default now(),
  finished_at  timestamptz
);
create index on operations (project_id, created_at desc);

create table log_lines (
  id           bigserial primary key,
  project_id   uuid not null references projects on delete cascade,
  server_id    uuid not null references servers on delete cascade,
  container_id text not null,
  container    text not null,
  app          text not null default '',
  ts           timestamptz not null,
  stream       text not null,
  level        text not null,
  line         text not null
);
create index on log_lines (project_id, ts desc);
create index log_lines_trgm on log_lines using gin (line gin_trgm_ops);

create table app_metrics (
  application_id uuid not null references applications on delete cascade,
  ts             timestamptz not null,
  requests       int not null,
  errors         int not null,
  avg_ms         real not null,
  p95_ms         real not null,
  primary key (application_id, ts)
);

create table secrets (
  id              uuid primary key default gen_random_uuid(),
  org_id          uuid not null references organizations on delete cascade,
  project_id      uuid references projects on delete cascade, -- null = organization-wide
  key             text not null,
  value_encrypted bytea not null,
  created_by      uuid references users on delete set null,
  updated_at      timestamptz not null default now(),
  created_at      timestamptz not null default now()
);
create unique index on secrets (org_id, coalesce(project_id, '00000000-0000-0000-0000-000000000000'), key);

create table api_keys (
  id           uuid primary key default gen_random_uuid(),
  org_id       uuid not null references organizations on delete cascade,
  project_id   uuid references projects on delete cascade, -- null = all projects
  name         text not null,
  key_hash     bytea not null unique,
  prefix       text not null,
  role         text not null check (role in ('viewer','member','admin')),
  created_by   uuid references users on delete cascade,
  last_used_at timestamptz,
  expires_at   timestamptz,
  revoked_at   timestamptz,
  created_at   timestamptz not null default now()
);
