import Link from '@docusaurus/Link';
import {useCallback, useEffect, useMemo, useRef, useState} from 'react';

import {searchEntries, type SearchEntry} from '@site/src/generated/search-index';
import styles from './styles.module.css';

const recommendedRoutes = new Set([
  '/operator/',
  '/institution-admin/',
  '/security/',
  '/developers/',
  '/api/',
  '/glossary/',
]);
const endpointCount = searchEntries.filter((entry) => entry.kind === 'endpoint').length;

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

function score(entry: SearchEntry, normalizedQuery: string): number {
  if (!normalizedQuery) {
    return recommendedRoutes.has(entry.href) ? 1 : 0;
  }
  const title = entry.title.toLowerCase();
  const group = entry.group.toLowerCase();
  const location = `${entry.href} ${entry.method ?? ''} ${entry.path ?? ''}`.toLowerCase();
  const tokens = normalizedQuery.split(/\s+/).filter(Boolean);
  if (!tokens.every((token) => entry.searchText.includes(token) || location.includes(token))) {
    return 0;
  }
  let total = entry.kind === 'guide' || entry.kind === 'glossary' ? 8 : 0;
  if (title === normalizedQuery) total += 120;
  if (title.startsWith(normalizedQuery)) total += 70;
  if (title.includes(normalizedQuery)) total += 45;
  if (group.includes(normalizedQuery)) total += 24;
  if (location.includes(normalizedQuery)) total += 35;
  for (const token of tokens) {
    if (title.includes(token)) total += 18;
    if (group.includes(token)) total += 8;
    if (location.includes(token)) total += 12;
    if (entry.searchText.includes(token)) total += 4;
  }
  return total;
}

function kindLabel(entry: SearchEntry): string {
  switch (entry.kind) {
    case 'endpoint':
      return 'Endpoint';
    case 'product-area':
      return 'Product area';
    case 'glossary':
      return 'Glossary';
    default:
      return 'Guide';
  }
}

export default function SearchBar(): React.JSX.Element {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const resultListRef = useRef<HTMLUListElement>(null);
  const [query, setQuery] = useState('');

  const openSearch = useCallback(() => {
    const dialog = dialogRef.current;
    if (!dialog || dialog.open) return;
    dialog.showModal();
    window.requestAnimationFrame(() => inputRef.current?.focus());
  }, []);

  const closeSearch = useCallback(() => dialogRef.current?.close(), []);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        openSearch();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [openSearch]);

  const matches = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return searchEntries
      .map((entry) => ({entry, score: score(entry, normalized)}))
      .filter((match) => match.score > 0)
      .sort(
        (left, right) =>
          right.score - left.score || left.entry.title.localeCompare(right.entry.title),
      )
      .slice(0, 12)
      .map((match) => match.entry);
  }, [query]);

  const focusResult = (index: number) => {
    const links = resultListRef.current?.querySelectorAll<HTMLAnchorElement>('a');
    if (!links || links.length === 0) return;
    links[Math.max(0, Math.min(index, links.length - 1))]?.focus();
  };

  return (
    <>
      <button
        aria-label="Search documentation"
        className={styles.trigger}
        onClick={openSearch}
        type="button">
        <SearchIcon />
        <span className={styles.triggerLabel}>Search docs</span>
        <kbd>⌘&nbsp;K</kbd>
      </button>

      <dialog
        aria-describedby="docs-search-scope"
        aria-labelledby="docs-search-title"
        className={styles.dialog}
        onCancel={closeSearch}
        onClose={() => setQuery('')}
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            event.preventDefault();
            closeSearch();
          }
        }}
        ref={dialogRef}>
        <div className={styles.header}>
          <div>
            <p className={styles.eyebrow}>Local documentation index</p>
            <h2 id="docs-search-title">Search Proctor Docs</h2>
            <p className={styles.scope} id="docs-search-scope">
              Guides, glossary terms, product areas, and all {endpointCount} endpoints.
            </p>
          </div>
          <button
            aria-label="Close documentation search"
            className={styles.close}
            onClick={closeSearch}
            type="button">
            <CloseIcon />
          </button>
        </div>

        <label className={styles.searchField}>
          <span className={styles.visuallyHidden}>Search documentation</span>
          <SearchIcon />
          <input
            autoComplete="off"
            name="docs-query"
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'ArrowDown') {
                event.preventDefault();
                focusResult(0);
              }
            }}
            placeholder="Search guides, terms, or GET /api/v1/…"
            ref={inputRef}
            spellCheck={false}
            type="search"
            value={query}
          />
        </label>

        <div className={styles.resultMeta}>
          <span>{query.trim() ? `${matches.length} best matches` : 'Recommended starting points'}</span>
          <span aria-hidden="true">↑↓ navigate · esc close</span>
        </div>

        <div aria-live="polite" className={styles.results}>
          {matches.length > 0 ? (
            <ul ref={resultListRef}>
              {matches.map((entry, index) => (
                <li key={entry.id}>
                  <Link
                    className={styles.result}
                    onClick={closeSearch}
                    onKeyDown={(event) => {
                      if (event.key === 'ArrowDown') {
                        event.preventDefault();
                        focusResult(index + 1);
                      } else if (event.key === 'ArrowUp') {
                        event.preventDefault();
                        if (index === 0) inputRef.current?.focus();
                        else focusResult(index - 1);
                      }
                    }}
                    to={entry.href}>
                    <span className={styles.resultContext}>
                      <span className={styles.kind}>{kindLabel(entry)}</span>
                      <span>{entry.group}</span>
                    </span>
                    <strong>{entry.title}</strong>
                    {entry.method && entry.path ? (
                      <span className={styles.endpoint}>
                        <b>{entry.method}</b>
                        <code>{entry.path}</code>
                      </span>
                    ) : (
                      <span className={styles.description}>{entry.description}</span>
                    )}
                    <span aria-hidden="true" className={styles.arrow}>→</span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <div className={styles.empty}>
              <strong>No documentation matches “{query.trim()}”.</strong>
              <span>Try a canonical term, task, endpoint method, or route fragment.</span>
            </div>
          )}
        </div>
      </dialog>
    </>
  );
}
