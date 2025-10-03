#!/bin/bash
set -e

echo "Setting up clean test database..."

# Create clean testdb
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
    DROP DATABASE IF EXISTS testdb;
    CREATE DATABASE testdb;
EOSQL

# Install schema using pgqueue CLI (PgQueuer-aligned approach)
echo "Installing schema using pgqueue CLI..."

# Simple sleep to ensure PostgreSQL is ready (init scripts run after PostgreSQL is up)
sleep 2

# In Docker container, use Unix socket connection
DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@/testdb?host=/var/run/postgresql&sslmode=disable"

# Use the CLI to install the schema - this is the single source of truth for schema
pgqueue install --database-url "$DATABASE_URL" --verbose

echo "Test database ready with schema installed via CLI!"