CREATE TABLE external_login_states (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    provider varchar(64) NOT NULL,
    state_hash char(64) NOT NULL,
    binding_hash char(64) NOT NULL,
    return_to varchar(2048) NOT NULL,
    client_type varchar(32) NOT NULL CHECK (client_type IN ('desktop', 'web')),
    device_id varchar(128) NOT NULL DEFAULT '',
    device_name varchar(512) NOT NULL DEFAULT '',
    expires_at bigint NOT NULL,
    consumed_at bigint NOT NULL DEFAULT 0,
    CONSTRAINT external_login_states_valid_lifetime
        CHECK (
            expires_at > create_at AND
            (consumed_at = 0 OR (consumed_at >= create_at AND consumed_at < expires_at))
        )
);

CREATE UNIQUE INDEX external_login_states_state_hash_key
    ON external_login_states (state_hash);

CREATE INDEX external_login_states_expiry_idx
    ON external_login_states (expires_at);
