-- Workflows: steps that run when a trigger fires. The definition is a JSON tree validated in workflows.go.
create table workflows (
  id                uuid primary key default gen_random_uuid(),
  project_id        uuid not null references projects on delete cascade,
  name              text not null,
  enabled           boolean not null default false,
  trigger_type      text not null, -- copy of definition->trigger->type, for matching events
  definition        jsonb not null,
  webhook_secret    text not null,
  saved_by          uuid references users on delete set null, -- runs act as this user, with their current role
  last_scheduled_at timestamptz not null default now(),
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);
create index on workflows (project_id, trigger_type) where enabled;

create table workflow_runs (
  id          uuid primary key default gen_random_uuid(),
  workflow_id uuid not null references workflows on delete cascade,
  status      text not null default 'running' check (status in ('running','succeeded','failed','skipped')),
  trigger     text not null,  -- event kind, or 'manual'
  data        jsonb not null default '{}',
  error       text not null default '',
  started_by  uuid references users on delete set null, -- manual runs
  created_at  timestamptz not null default now(),
  finished_at timestamptz
);
create index on workflow_runs (workflow_id, created_at desc);
-- At most one running run per workflow: further triggers are recorded as skipped instead of piling up.
create unique index workflow_runs_one_running on workflow_runs (workflow_id) where status = 'running';

create table workflow_run_steps (
  id          bigserial primary key,
  run_id      uuid not null references workflow_runs on delete cascade,
  path        text not null,  -- position in the tree, e.g. "2.then.0"
  type        text not null,
  title       text not null,
  status      text not null default 'running' check (status in ('running','succeeded','failed')),
  branch      text not null default '', -- If: 'then' or 'else'; a failed step: 'on_failure' when its branch ran
  output      text not null default '',
  error       text not null default '',
  started_at  timestamptz not null default now(),
  finished_at timestamptz
);
create index on workflow_run_steps (run_id, id);

-- Servers currently at or above the disk threshold, so "disk nearly full" fires once per crossing.
create table workflow_disk_full (
  server_id uuid primary key references servers on delete cascade
);
