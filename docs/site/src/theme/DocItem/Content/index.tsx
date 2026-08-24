import {useDoc} from '@docusaurus/plugin-content-docs/client';
import OriginalDocItemContent from '@theme-original/DocItem/Content';
import type {Props} from '@theme/DocItem/Content';
import type {ReactNode} from 'react';

import styles from './styles.module.css';

const audienceLabels: Record<string, string> = {
  everyone: 'All readers',
  operator: 'Operator guide',
  'institution-administrator': 'Institution administrator guide',
  'security-reviewer': 'Security review guide',
  developer: 'Developer guide',
  'api-consumer': 'API consumer guide',
};

const maturityLabels: Record<string, string> = {
  available: 'Available',
  preview: 'Preview guidance',
  planned: 'Planned',
};

export default function DocItemContent({children}: Props): ReactNode {
  const {frontMatter} = useDoc();
  const guideFrontMatter = frontMatter as typeof frontMatter & {
    audience?: unknown;
    maturity?: unknown;
  };
  const audience =
    typeof guideFrontMatter.audience === 'string' ? guideFrontMatter.audience : '';
  const maturity =
    typeof guideFrontMatter.maturity === 'string' ? guideFrontMatter.maturity : '';
  const showGuideMeta = Boolean(audience && maturity && !frontMatter.hide_title);

  return (
    <>
      {showGuideMeta ? (
        <aside aria-label="Guide scope and maturity" className={styles.meta}>
          <span>{audienceLabels[audience] ?? audience}</span>
          <span aria-hidden="true" className={styles.divider}>/</span>
          <span className={styles[maturity]}>
            <i aria-hidden="true" />
            {maturityLabels[maturity] ?? maturity}
          </span>
        </aside>
      ) : null}
      <OriginalDocItemContent>{children}</OriginalDocItemContent>
    </>
  );
}
