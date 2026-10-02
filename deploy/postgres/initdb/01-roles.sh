#!/bin/sh
# Runs once, on the first start of an empty data directory.
# Creates the migration owner, the least-privilege application role and the
# wallet database. Table privileges are granted by the migrations.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<EOSQL
CREATE ROLE wallet_owner LOGIN CREATEDB PASSWORD '${WALLET_OWNER_PASSWORD}';
CREATE ROLE wallet_app LOGIN PASSWORD '${WALLET_APP_PASSWORD}';
CREATE DATABASE wallet OWNER wallet_owner;
REVOKE ALL ON DATABASE wallet FROM PUBLIC;
GRANT CONNECT ON DATABASE wallet TO wallet_app;
EOSQL

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname wallet <<EOSQL
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO wallet_app;
EOSQL
