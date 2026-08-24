import Link from '@docusaurus/Link';
import styles from './styles.module.css';

const roles = [
  {
    label: 'Operate',
    audience: 'Operator',
    title: 'Run a deployment',
    description: 'Plan dependencies, configuration, health, scaling, and recovery.',
    to: '/operator/',
  },
  {
    label: 'Administer',
    audience: 'Institution administrator',
    title: 'Shape the institution',
    description: 'Build the academic hierarchy, identities, roles, and access policy.',
    to: '/institution-admin/',
  },
  {
    label: 'Review & secure',
    audience: 'Security reviewer',
    title: 'Trace the guarantees',
    description: 'Review credentials, authorization, audit, data, and isolation boundaries.',
    to: '/security/',
  },
  {
    label: 'Build & integrate',
    audience: 'Developer',
    title: 'Contribute or integrate',
    description: 'Understand the modules, contracts, development workflow, and API.',
    to: '/developers/',
  },
];

export default function RoleGrid(): React.JSX.Element {
  return (
    <div className={styles.grid}>
      {roles.map((role) => (
        <Link className={styles.card} to={role.to} key={role.label}>
          <span className={styles.label}>{role.label}</span>
          <span className={styles.audience}>{role.audience}</span>
          <strong>{role.title}</strong>
          <span className={styles.description}>{role.description}</span>
          <span className={styles.action} aria-hidden="true">
            Open guide <span>→</span>
          </span>
        </Link>
      ))}
    </div>
  );
}
