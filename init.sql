-- This script runs once when the Postgres container initializes for the
-- first time (via the /docker-entrypoint-initdb.d/ convention).
-- It creates a separate test database so integration tests don't touch
-- the main fplbanterbot database.
--
-- If the pgdata volume already exists, Postgres skips initdb scripts.
-- To re-run this, destroy the volume: docker compose down -v
CREATE DATABASE fplbanterbot_test;
