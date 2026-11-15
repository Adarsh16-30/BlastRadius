# PostgreSQL 17 + repmgr + pgBackRest, one image for both nodes (PRD 6.4).
# Base tag postgres:17.9-bookworm verified with `docker manifest inspect` on 2026-11-15.
# repmgr 5.5.0 (supports PostgreSQL 13-18): https://www.repmgr.org/docs/current/
# Packages come from the PGDG apt repository the official image already configures.
FROM postgres:17.9-bookworm

# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends postgresql-17-repmgr pgbackrest \
 && rm -rf /var/lib/apt/lists/*

COPY postgresql.conf pg_hba.conf /etc/postgresql/
COPY repmgr.conf.tmpl /etc/repmgr.conf.tmpl
COPY --chmod=0755 entrypoint.sh /usr/local/bin/entrypoint.sh

# Rendered at start on tmpfs; set here so `docker exec` sessions find them too.
ENV PGPASSFILE=/tmp/pgpass \
    REPMGR_CONF=/tmp/repmgr.conf

USER postgres
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD []
