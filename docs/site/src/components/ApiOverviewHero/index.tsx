import Link from '@docusaurus/Link';

import {searchEntries} from '@site/src/generated/search-index';
import styles from './styles.module.css';

export default function ApiOverviewHero(): React.JSX.Element {
  const endpointCount = searchEntries.filter((entry) => entry.kind === 'endpoint').length;
  const productAreaCount = searchEntries.filter(
    (entry) => entry.kind === 'product-area',
  ).length;

  return (
    <header className={styles.hero}>
      <div className={styles.eyebrow}>Reviewed OpenAPI contract</div>
      <h1>Proctor API</h1>
      <p>
        Integrate with one explicit contract for authentication, assurance,
        idempotency, stable failures, protected content, and realtime recovery.
      </p>
      <div className={styles.actions}>
        <Link className="button button--primary" to="/api/guides/getting-started">
          Start an integration
        </Link>
        <a className="button button--secondary" download href="/openapi/openapi.json">
          Download OpenAPI
        </a>
      </div>
      <dl className={styles.record} aria-label="API reference coverage">
        <div>
          <dt>Specification</dt>
          <dd>OpenAPI 3.1</dd>
        </div>
        <div>
          <dt>Reference</dt>
          <dd>{endpointCount} operations / {productAreaCount} product areas</dd>
        </div>
        <div>
          <dt>Console policy</dt>
          <dd>Request sending disabled</dd>
        </div>
      </dl>
    </header>
  );
}
