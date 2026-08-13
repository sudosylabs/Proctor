-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE installation_states
    ADD CONSTRAINT installation_states_institution_id_canonical_check
        CHECK (institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT installation_states_administrator_user_id_canonical_check
        CHECK (administrator_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
