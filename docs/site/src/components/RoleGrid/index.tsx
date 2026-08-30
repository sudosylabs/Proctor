import Link from '@docusaurus/Link';
import DocIcon, {type DocIconName} from '@site/src/components/DocIcon';
import styles from './styles.module.css';

const roles: Array<{
  label: string;
  audience: string;
  title: string;
  description: string;
  to: string;
  icon: DocIconName;
}> = [
  {
    icon: 'account',
    label: 'Use your account',
    audience: 'Account holder',
    title: 'Sign in with confidence',
    description: 'Access, recover, protect, and review the sessions connected to your account.',
    to: '/account/',
  },
  {
    icon: 'deployment',
    label: 'Operate',
    audience: 'Operator',
    title: 'Run a deployment',
    description: 'Plan dependencies, configuration, health, scaling, and recovery.',
    to: '/operator/',
  },
  {
    icon: 'institution',
    label: 'Administer',
    audience: 'Institution administrator',
    title: 'Shape the institution',
    description: 'Build the academic hierarchy, identities, roles, and access policy.',
    to: '/institution-admin/',
  },
  {
    icon: 'exam',
    label: 'Manage exams',
    audience: 'Exam Manager',
    title: 'Author protected delivery',
    description: 'Build immutable revisions, manage sittings, and review terminal submissions.',
    to: '/exam-manager/',
  },
  {
    icon: 'candidate',
    label: 'Take an exam',
    audience: 'Candidate',
    title: 'Protect your attempt',
    description: 'Prepare Desktop, keep work acknowledged, recover continuity, and submit safely.',
    to: '/candidate/',
  },
  {
    icon: 'assurance',
    label: 'Review & secure',
    audience: 'Security reviewer',
    title: 'Trace the guarantees',
    description: 'Review credentials, authorization, audit, data, and isolation boundaries.',
    to: '/security/',
  },
  {
    icon: 'integration',
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
          <span className={styles.label}>
            <DocIcon name={role.icon} size={32} />
            <span>{role.label}</span>
          </span>
          <span className={styles.audience}>{role.audience}</span>
          <strong>{role.title}</strong>
          <span className={styles.description}>{role.description}</span>
          <span className={styles.action} aria-hidden="true">
            Open guide <DocIcon name="arrowRight" />
          </span>
        </Link>
      ))}
    </div>
  );
}
