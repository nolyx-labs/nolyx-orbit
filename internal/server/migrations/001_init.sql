create table users (
  id            uuid primary key default gen_random_uuid(),
  email         text not null unique,
  name          text not null,
  password_hash text not null,
  created_at    timestamptz not null default now()
);

create table sessions (
  token_hash bytea primary key,
  user_id    uuid not null references users on delete cascade,
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);

create table organizations (
  id         uuid primary key default gen_random_uuid(),
  name       text not null,
  created_at timestamptz not null default now()
);

create table memberships (
  org_id  uuid not null references organizations on delete cascade,
  user_id uuid not null references users on delete cascade,
  role    text not null check (role in ('owner','admin','member','viewer')),
  primary key (org_id, user_id)
);

create table projects (
  id         uuid primary key default gen_random_uuid(),
  org_id     uuid not null references organizations on delete cascade,
  name       text not null,
  created_at timestamptz not null default now(),
  unique (org_id, name)
);

create table enrollment_tokens (
  id         uuid primary key default gen_random_uuid(),
  project_id uuid not null references projects on delete cascade,
  token_hash bytea not null unique,
  created_by uuid references users on delete set null,
  expires_at timestamptz not null,
  revoked_at timestamptz,
  last_used_at timestamptz,
  created_at timestamptz not null default now()
);

create table servers (
  id                uuid primary key default gen_random_uuid(),
  project_id        uuid not null references projects on delete cascade,
  name              text not null,
  hostname          text not null default '',
  ip                text not null default '',
  os                text not null default '',
  arch              text not null default '',
  kernel            text not null default '',
  cpu_count         int not null default 0,
  mem_total         bigint not null default 0,
  disk_total        bigint not null default 0,
  agent_version     text not null default '',
  docker_version    text not null default '',
  agent_secret_hash bytea not null,
  status            text not null default 'offline' check (status in ('online','offline')),
  last_seen_at      timestamptz,
  created_at        timestamptz not null default now()
);
create index on servers (project_id);

create table server_metrics (
  server_id  uuid not null references servers on delete cascade,
  ts         timestamptz not null,
  cpu        real not null,
  mem_used   bigint not null,
  mem_total  bigint not null,
  disk_used  bigint not null,
  disk_total bigint not null,
  net_rx     bigint not null,
  net_tx     bigint not null,
  load1      real not null,
  uptime     bigint not null,
  primary key (server_id, ts)
);

create table containers (
  server_id     uuid not null references servers on delete cascade,
  docker_id     text not null,
  name          text not null,
  image         text not null,
  state         text not null,
  status        text not null,
  health        text not null default '',
  created       timestamptz not null,
  ports         jsonb not null default '[]',
  labels        jsonb not null default '{}',
  restart_count int not null default 0,
  cpu           real not null default 0,
  mem_used      bigint not null default 0,
  mem_limit     bigint not null default 0,
  updated_at    timestamptz not null default now(),
  primary key (server_id, docker_id)
);

create table container_metrics (
  server_id uuid not null references servers on delete cascade,
  docker_id text not null,
  ts        timestamptz not null,
  cpu       real not null,
  mem_used  bigint not null,
  primary key (server_id, docker_id, ts)
);

create table commands (
  id           uuid primary key,
  server_id    uuid not null references servers on delete cascade,
  action       text not null,
  args         jsonb not null,
  status       text not null default 'pending' check (status in ('pending','succeeded','failed')),
  error        text,
  requested_by uuid references users on delete set null,
  created_at   timestamptz not null default now(),
  completed_at timestamptz
);

create table applications (
  id            uuid primary key default gen_random_uuid(),
  project_id    uuid not null references projects on delete cascade,
  server_id     uuid references servers on delete set null,
  name          text not null,
  image         text not null,
  ports         jsonb not null default '[]',
  env_encrypted bytea,
  env_keys      jsonb not null default '[]',
  health_path   text not null default '',
  created_at    timestamptz not null default now(),
  unique (project_id, name)
);

create table deployments (
  id             uuid primary key default gen_random_uuid(),
  application_id uuid not null references applications on delete cascade,
  number         int not null,
  server_id      uuid references servers on delete set null,
  image          text not null,
  status         text not null default 'running' check (status in ('running','succeeded','failed')),
  steps          jsonb not null default '{}',
  log            text not null default '',
  error          text,
  triggered_by   uuid references users on delete set null,
  created_at     timestamptz not null default now(),
  finished_at    timestamptz,
  unique (application_id, number)
);

create table events (
  id         bigserial primary key,
  project_id uuid not null references projects on delete cascade,
  server_id  uuid references servers on delete set null,
  severity   text not null check (severity in ('info','warning','error')),
  message    text not null,
  created_at timestamptz not null default now()
);
create index on events (project_id, created_at desc);

create table audit_events (
  id         bigserial primary key,
  org_id     uuid not null references organizations on delete cascade,
  project_id uuid references projects on delete set null,
  actor_id   uuid references users on delete set null,
  actor      text not null,
  action     text not null,
  target     text not null,
  metadata   jsonb not null default '{}',
  ip         text not null default '',
  created_at timestamptz not null default now()
);
create index on audit_events (org_id, created_at desc);

create table alert_rules (
  id               uuid primary key default gen_random_uuid(),
  project_id       uuid not null references projects on delete cascade,
  name             text not null,
  metric           text not null check (metric in ('cpu','memory','disk','offline')),
  threshold        real not null default 0,
  duration_minutes int not null default 5 check (duration_minutes between 1 and 1440),
  webhook_url      text not null default '',
  created_at       timestamptz not null default now()
);

create table alert_incidents (
  id          bigserial primary key,
  rule_id     uuid not null references alert_rules on delete cascade,
  server_id   uuid not null references servers on delete cascade,
  value       real not null,
  opened_at   timestamptz not null default now(),
  resolved_at timestamptz
);
create unique index on alert_incidents (rule_id, server_id) where resolved_at is null;
