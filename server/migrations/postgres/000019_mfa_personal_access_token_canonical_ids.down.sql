ALTER TABLE personal_access_tokens
    DROP CONSTRAINT personal_access_tokens_academic_unit_id_canonical_check,
    DROP CONSTRAINT personal_access_tokens_user_id_canonical_check,
    DROP CONSTRAINT personal_access_tokens_id_canonical_check;

ALTER TABLE mfa_recovery_codes
    DROP CONSTRAINT mfa_recovery_codes_user_id_canonical_check,
    DROP CONSTRAINT mfa_recovery_codes_id_canonical_check;

ALTER TABLE mfa_credentials
    DROP CONSTRAINT mfa_credentials_user_id_canonical_check,
    DROP CONSTRAINT mfa_credentials_id_canonical_check;
