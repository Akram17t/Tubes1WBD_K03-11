CREATE UNIQUE INDEX database_connections_target_key
    ON database_connections (lower(btrim(host)), port, database_name);
