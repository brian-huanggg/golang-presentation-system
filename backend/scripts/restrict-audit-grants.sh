#!/usr/bin/env bash
# Restricts the application DB user to INSERT and SELECT on audit_record (AUD-003,
# Architecture 7.2). Run as the DB administrator after `cmd/migrate`; idempotent.
#
# Why table-level grants: MariaDB privileges are additive, so a database-level grant
# (the docker-compose image gives MARIADB_USER ALL on the database) would always
# include UPDATE/DELETE on audit_record. We drop the database-level grant and grant
# table by table instead. Each feature ticket adds its own tables to APP_TABLES.
#
# Usage (dev), from the repo root:
#   docker compose exec -T mariadb bash -s < backend/scripts/restrict-audit-grants.sh
# The root password and names come from the container's environment, never argv.
set -euo pipefail

: "${MARIADB_ROOT_PASSWORD:?administrator password required}"
: "${MARIADB_DATABASE:?database name required}"
: "${MARIADB_USER:?application user required}"

# Tables the app may fully read and write. audit_record is deliberately absent.
APP_TABLES=(user sessions)

sql="REVOKE ALL PRIVILEGES, GRANT OPTION FROM '${MARIADB_USER}'@'%';
GRANT USAGE ON *.* TO '${MARIADB_USER}'@'%';
GRANT INSERT, SELECT ON \`${MARIADB_DATABASE}\`.\`audit_record\` TO '${MARIADB_USER}'@'%';
"
for t in "${APP_TABLES[@]}"; do
  sql+="GRANT SELECT, INSERT, UPDATE, DELETE ON \`${MARIADB_DATABASE}\`.\`${t}\` TO '${MARIADB_USER}'@'%';
"
done
sql+="FLUSH PRIVILEGES;"

# MYSQL_PWD keeps the password out of the process list.
MYSQL_PWD="$MARIADB_ROOT_PASSWORD" mariadb -uroot --batch <<<"$sql"
