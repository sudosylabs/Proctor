import Link from '@docusaurus/Link';
import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styles from './styles.module.css';

const guides = [
  {
    group: 'Start',
    title: 'Documentation Home',
    description: 'Choose a guide by responsibility or product boundary.',
    to: '/',
    keywords: 'overview introduction start home',
  },
  {
    group: 'Operate',
    title: 'Deployment Overview',
    description: 'Understand dependencies, deployment shapes, and readiness.',
    to: '/operator/',
    keywords: 'install deployment configuration health cluster backup operator',
  },
  {
    group: 'Administer',
    title: 'Institution Setup',
    description: 'Establish academic structure, identity, and scoped access.',
    to: '/institution-admin/',
    keywords: 'institution academic class programme role onboarding admin',
  },
  {
    group: 'Review & Secure',
    title: 'Security Overview',
    description: 'Trace identity, authorization, audit, data, and execution boundaries.',
    to: '/security/',
    keywords: 'security identity audit tls credentials authorization retention',
  },
  {
    group: 'Build & Integrate',
    title: 'Developer Guide',
    description: 'Find module boundaries, contracts, and contribution checks.',
    to: '/developers/',
    keywords: 'developer contribute modules go react architecture build',
  },
  {
    group: 'Build & Integrate',
    title: 'API Reference',
    description: 'Download and interpret the reviewed OpenAPI contract.',
    to: '/api/',
    keywords: 'api openapi endpoint authentication errors integration',
  },
];

function SearchIcon(): React.JSX.Element {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20">
      <circle cx="8.5" cy="8.5" r="5.75" />
      <path d="m13 13 4 4" />
    </svg>
  );
}

function CloseIcon(): React.JSX.Element {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20">
      <path d="m4 4 12 12M16 4 4 16" />
    </svg>
  );
}

export default function SearchBar(): React.JSX.Element {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState('');

  const openFinder = useCallback(() => {
    const dialog = dialogRef.current;
    if (!dialog || dialog.open) {
      return;
    }

    dialog.showModal();
    window.requestAnimationFrame(() => inputRef.current?.focus());
  }, []);

  const closeFinder = useCallback(() => {
    dialogRef.current?.close();
  }, []);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        openFinder();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [openFinder]);

  const matches = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    if (!normalizedQuery) {
      return guides;
    }

    return guides.filter((guide) =>
      `${guide.group} ${guide.title} ${guide.description} ${guide.keywords}`
        .toLowerCase()
        .includes(normalizedQuery),
    );
  }, [query]);

  return (
    <>
      <button
        aria-label="Open guide finder"
        className={styles.trigger}
        onClick={openFinder}
        type="button">
        <SearchIcon />
        <span className={styles.triggerLabel}>Find a guide</span>
        <kbd>⌘&nbsp;K</kbd>
      </button>

      <dialog
        aria-labelledby="guide-finder-title"
        className={styles.dialog}
        onClose={() => setQuery('')}
        ref={dialogRef}>
        <div className={styles.header}>
          <div>
            <p className={styles.eyebrow}>Quick navigation</p>
            <h2 id="guide-finder-title">Find a Guide</h2>
          </div>
          <button
            aria-label="Close guide finder"
            className={styles.close}
            onClick={closeFinder}
            type="button">
            <CloseIcon />
          </button>
        </div>

        <label className={styles.searchField}>
          <span className={styles.visuallyHidden}>Filter guides</span>
          <SearchIcon />
          <input
            autoComplete="off"
            name="guide-query"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Try “deployment” or “API”…"
            ref={inputRef}
            spellCheck={false}
            type="search"
            value={query}
          />
        </label>

        <div aria-live="polite" className={styles.results}>
          {matches.length > 0 ? (
            <ul>
              {matches.map((guide) => (
                <li key={guide.to}>
                  <Link className={styles.result} onClick={closeFinder} to={guide.to}>
                    <span className={styles.group}>{guide.group}</span>
                    <strong>{guide.title}</strong>
                    <span className={styles.description}>{guide.description}</span>
                    <span aria-hidden="true" className={styles.arrow}>→</span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <div className={styles.empty}>
              <strong>No matching guide</strong>
              <span>Try a responsibility, feature, or system boundary.</span>
            </div>
          )}
        </div>
      </dialog>
    </>
  );
}
