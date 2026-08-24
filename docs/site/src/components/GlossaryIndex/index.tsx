import {useMemo, useState} from 'react';

import {glossaryTerms} from '@site/src/generated/glossary';
import styles from './styles.module.css';

export default function GlossaryIndex(): React.JSX.Element {
  const [query, setQuery] = useState('');
  const matches = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) {
      return glossaryTerms;
    }
    return glossaryTerms.filter((entry) =>
      `${entry.term} ${entry.section} ${entry.definition} ${entry.avoid}`
        .toLowerCase()
        .includes(normalized),
    );
  }, [query]);
  const sections = [...new Set(matches.map((entry) => entry.section))];

  return (
    <div className={styles.glossary}>
      <label className={styles.filter}>
        <span>Filter canonical terms</span>
        <input
          autoComplete="off"
          name="glossary-query"
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Try “workspace”, “identity”, or “exam”…"
          spellCheck={false}
          type="search"
          value={query}
        />
      </label>
      <p aria-live="polite" className={styles.count}>
        Showing {matches.length} of {glossaryTerms.length} terms
      </p>
      {sections.map((section) => (
        <section className={styles.section} key={section}>
          <h2>{section}</h2>
          <dl>
            {matches
              .filter((entry) => entry.section === section)
              .map((entry) => (
                <div className={styles.entry} id={entry.id} key={entry.id}>
                  <dt>
                    <a aria-label={`Link to ${entry.term}`} href={`#${entry.id}`}>#</a>
                    {entry.term}
                  </dt>
                  <dd>
                    <p>{entry.definition}</p>
                    <p className={styles.avoid}><strong>Avoid:</strong> {entry.avoid}</p>
                  </dd>
                </div>
              ))}
          </dl>
        </section>
      ))}
      {matches.length === 0 ? (
        <div className={styles.empty}>
          <strong>No canonical term matches “{query.trim()}”.</strong>
          <span>Try a broader product concept or clear the filter.</span>
        </div>
      ) : null}
    </div>
  );
}
