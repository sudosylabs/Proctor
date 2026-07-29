CREATE TABLE installation_state (
    singleton smallint PRIMARY KEY CHECK (singleton = 1),
    initialized_at bigint NOT NULL CHECK (initialized_at > 0),
    institution_id varchar(26) NOT NULL REFERENCES institutions(id),
    administrator_user_id varchar(26) NOT NULL REFERENCES users(id)
);
