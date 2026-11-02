-- Service volumes, command and container health check (used by the app catalog, available to every Docker service).
alter table applications
  add column volumes    jsonb not null default '[]', -- "name:/container/path"; Docker volume orbit-<service>-<name>, kept on delete
  add column command    jsonb not null default '[]', -- replaces the image's CMD when non-empty
  add column health_cmd text  not null default '';   -- Docker HEALTHCHECK (shell); deploys wait for healthy
