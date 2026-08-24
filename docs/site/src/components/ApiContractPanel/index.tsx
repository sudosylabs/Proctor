import React, {type JSX} from 'react';
import {useDoc} from '@docusaurus/plugin-content-docs/client';
import styles from './styles.module.css';

type ApiContract = {
  'x-proctor-auth'?: string;
  'x-proctor-error-codes'?: string[];
  'x-proctor-idempotency'?: string;
};

type ApiFrontMatter = {
  api?: ApiContract;
};

const authenticationLabels: Record<string, string> = {
  public: 'No credential required',
  principal_required: 'Authenticated principal required',
  session_required: 'Interactive Session required',
  recent_session_required: 'Recent interactive Session required',
  strong_recent_session_required: 'Strong, recent interactive Session required',
  refresh_credential_required: 'Refresh credential required',
};

const idempotencyLabels: Record<string, string> = {
  none: 'No Idempotency-Key contract',
  optional: 'Idempotency-Key supported',
  required: 'Idempotency-Key required',
};

function humanize(value: string): string {
  return value.replaceAll('_', ' ').replace(/^./, (first) => first.toUpperCase());
}

export default function ApiContractPanel(): JSX.Element | null {
  const {frontMatter} = useDoc() as {frontMatter: ApiFrontMatter};
  const api = frontMatter.api;

  if (!api) {
    return null;
  }

  const authentication = api['x-proctor-auth'];
  const idempotency = api['x-proctor-idempotency'];
  const errorCodes = api['x-proctor-error-codes'] ?? [];

  return (
    <aside className={styles.panel} aria-labelledby="api-contract-heading">
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>Proctor contract</p>
          <h2 className={styles.title} id="api-contract-heading">
            Request requirements
          </h2>
        </div>
        <span className={styles.source}>OpenAPI</span>
      </header>

      <dl className={styles.requirements}>
        <div className={styles.requirement}>
          <dt>Authentication</dt>
          <dd>
            {authentication
              ? authenticationLabels[authentication] ?? humanize(authentication)
              : 'Not declared'}
          </dd>
        </div>
        <div className={styles.requirement}>
          <dt>Idempotency</dt>
          <dd>
            {idempotency
              ? idempotencyLabels[idempotency] ?? humanize(idempotency)
              : 'Not declared'}
          </dd>
        </div>
      </dl>

      <details className={styles.errors}>
        <summary>
          {errorCodes.length} stable Problem Details{' '}
          {errorCodes.length === 1 ? 'code' : 'codes'}
        </summary>
        {errorCodes.length > 0 ? (
          <ul className={styles.errorList}>
            {errorCodes.map((code) => (
              <li key={code}>
                <code>{code}</code>
              </li>
            ))}
          </ul>
        ) : (
          <p className={styles.empty}>No application error codes are declared.</p>
        )}
      </details>
    </aside>
  );
}
