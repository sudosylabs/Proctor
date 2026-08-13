ALTER TABLE mfa_credentials
    ADD CONSTRAINT mfa_credentials_id_canonical_check
        CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT mfa_credentials_user_id_canonical_check
        CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE mfa_recovery_codes
    ADD CONSTRAINT mfa_recovery_codes_id_canonical_check
        CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT mfa_recovery_codes_user_id_canonical_check
        CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE personal_access_tokens
    ADD CONSTRAINT personal_access_tokens_id_canonical_check
        CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_tokens_user_id_canonical_check
        CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_tokens_academic_unit_id_canonical_check
        CHECK (academic_unit_id IS NULL OR academic_unit_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
