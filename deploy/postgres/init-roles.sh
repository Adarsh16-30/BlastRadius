#!/bin/sh
# Runs once on first init of an empty data directory (docker-entrypoint-initdb.d).
# Creates the least-privilege roles from PRD 6.4. Passwords come from the environment.
# shortcut: M1 uses the stock postgres image; repmgr and the repmgr role arrive in M2.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v app_pw="$APP_PASSWORD" -v mig_pw="$MIGRATOR_PASSWORD" -v probe_pw="$PROBE_PASSWORD" <<'SQL'
CREATE ROLE migrator LOGIN PASSWORD :'mig_pw';
CREATE ROLE app      LOGIN PASSWORD :'app_pw';
CREATE ROLE probe    LOGIN PASSWORD :'probe_pw';
GRANT pg_monitor TO probe;
GRANT CREATE, USAGE ON SCHEMA public TO migrator;
GRANT USAGE ON SCHEMA public TO app;
-- Tables are created (and owned) by migrator; app gets DML only, never DDL.
ALTER DEFAULT PRIVILEGES FOR ROLE migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app;
SQL
