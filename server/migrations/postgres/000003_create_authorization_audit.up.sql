CREATE UNIQUE INDEX role_bindings_active_grant_key
    ON role_bindings (user_id, role_id, scope_type, scope_id)
    WHERE delete_at = 0 AND end_at = 0;

CREATE INDEX role_bindings_active_user_time_idx
    ON role_bindings (user_id, start_at, end_at)
    WHERE delete_at = 0;

CREATE TABLE audit_events (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    actor_id varchar(26) REFERENCES users(id),
    session_id varchar(26) REFERENCES sessions(id),
    action varchar(128) NOT NULL,
    resource_type varchar(32) NOT NULL
        CHECK (resource_type IN ('institution', 'academic_unit', 'class')),
    resource_id varchar(26) NOT NULL,
    scope_type varchar(32) NOT NULL
        CHECK (scope_type IN ('institution', 'academic_unit', 'class')),
    scope_id varchar(26) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('attempt', 'success', 'fail')),
    request_id varchar(128) NOT NULL DEFAULT '',
    node_id varchar(128) NOT NULL,
    client_type varchar(32) NOT NULL DEFAULT '',
    authentication_method varchar(64) NOT NULL DEFAULT '',
    ip_address varchar(64) NOT NULL DEFAULT '',
    user_agent varchar(512) NOT NULL DEFAULT '',
    error_code varchar(128) NOT NULL DEFAULT '',
    parameters jsonb,
    prior_state jsonb,
    result jsonb
);

CREATE INDEX audit_events_created_idx ON audit_events (create_at DESC, id DESC);
CREATE INDEX audit_events_actor_created_idx
    ON audit_events (actor_id, create_at DESC, id DESC)
    WHERE actor_id IS NOT NULL;
CREATE INDEX audit_events_action_created_idx
    ON audit_events (action, create_at DESC, id DESC);
CREATE INDEX audit_events_resource_created_idx
    ON audit_events (resource_type, resource_id, create_at DESC, id DESC);
CREATE INDEX audit_events_incomplete_idx
    ON audit_events (create_at, id) WHERE status = 'attempt';
