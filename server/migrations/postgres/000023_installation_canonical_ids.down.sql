ALTER TABLE installation_states
    DROP CONSTRAINT installation_states_administrator_user_id_canonical_check,
    DROP CONSTRAINT installation_states_institution_id_canonical_check;
