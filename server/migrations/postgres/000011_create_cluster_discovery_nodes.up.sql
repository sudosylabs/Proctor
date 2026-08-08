-- Short-lived bootstrap discovery records for multi-node clustering.
-- PostgreSQL is not a cluster message bus; these rows only advertise join
-- addresses and protocol compatibility until Memberlist membership takes over.
CREATE TABLE cluster_discovery_nodes (
    node_id varchar(128) PRIMARY KEY,
    advertise_address varchar(512) NOT NULL,
    server_version varchar(128) NOT NULL,
    protocol_min integer NOT NULL,
    protocol_max integer NOT NULL,
    expires_at bigint NOT NULL,
    updated_at bigint NOT NULL,
    CONSTRAINT cluster_discovery_nodes_protocol_range_check
        CHECK (protocol_min > 0 AND protocol_max >= protocol_min),
    CONSTRAINT cluster_discovery_nodes_lifetime_check
        CHECK (expires_at > updated_at),
    CONSTRAINT cluster_discovery_nodes_advertise_address_check
        CHECK (char_length(btrim(advertise_address)) > 0)
);

CREATE INDEX cluster_discovery_nodes_expires_at_idx
    ON cluster_discovery_nodes (expires_at);

CREATE INDEX cluster_discovery_nodes_live_idx
    ON cluster_discovery_nodes (expires_at, node_id);
