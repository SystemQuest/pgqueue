#!/bin/bash
set -e

echo "Setting up clean test database..."

# Create clean testdb
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
    DROP DATABASE IF EXISTS testdb;
    CREATE DATABASE testdb;
EOSQL

# Install schema from migration file (single source of truth)
echo "Installing schema from migration file..."
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname testdb -f /migrations/001_initial_schema.up.sql

echo "Test database ready with schema installed!"