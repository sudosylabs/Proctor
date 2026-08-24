import type {ReactNode} from 'react';
import styles from './styles.module.css';

type MaturityState = 'available' | 'preview' | 'planned';

interface MaturityProps {
  children: ReactNode;
  state: MaturityState;
}

const labels: Record<MaturityState, string> = {
  available: 'Available',
  preview: 'Preview guidance',
  planned: 'Planned',
};

export default function Maturity({children, state}: MaturityProps): React.JSX.Element {
  return (
    <aside className={`${styles.notice} ${styles[state]}`} aria-label={labels[state]}>
      <strong><i aria-hidden="true"></i>{labels[state]}</strong>
      <div>{children}</div>
    </aside>
  );
}
