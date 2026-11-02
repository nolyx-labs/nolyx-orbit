-- Services imported from an existing container: settings carried over from it (never editable, see protocol.Carried)
-- and the container the first successful deploy replaces, which is then kept stopped as a fallback.
alter table applications
  add column carried          jsonb not null default '{}',
  add column import_container text  not null default '';

-- systemd services whose journal is collected with the container logs.
alter table servers add column log_units text[] not null default '{}';
alter table log_lines add column unit text not null default '';

-- Copying a project's logs into a PostgreSQL database the customer owns.
create table log_destinations (
  project_id    uuid primary key references projects on delete cascade,
  url_encrypted bytea not null,
  enabled       boolean not null default true,
  forwarded_id  bigint not null default 0, -- last log_lines id copied
  safe_id       bigint not null default 0, -- highest id seen a tick ago; lower ids have committed
  last_at       timestamptz,
  last_error    text not null default '',
  created_at    timestamptz not null default now()
);

-- Monitors can also be checked from servers; the control plane still checks every monitor.
alter table monitors add column locations uuid[] not null default '{}';
alter table monitor_checks add column location uuid references servers on delete cascade; -- null: the control plane
create index on monitor_checks (monitor_id, location, ts desc) where location is not null;
