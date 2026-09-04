import Link from '@docusaurus/Link';
import {guides} from '@site/navigation.mjs';
import DocIcon from '@site/src/components/DocIcon';
import styles from './styles.module.css';

const groups = [
  {id: 'use', title: 'Using Proctor'},
  {id: 'build', title: 'Running and building Proctor'},
];

export default function RoleGrid(): React.JSX.Element {
  return (
    <div className={styles.directory}>
      {groups.map((group) => (
        <section key={group.id} aria-labelledby={`guides-${group.id}`}>
          <h3 id={`guides-${group.id}`}>{group.title}</h3>
          <ul className={styles.list}>
            {guides.filter((guide) => guide.group === group.id).map((guide) => (
              <li key={guide.to}>
                <Link className={styles.link} to={guide.to}>
                  <span className={styles.title}>{guide.label}</span>
                  <span className={styles.description}>{guide.description}</span>
                  <DocIcon className={styles.arrow} name="arrowRight" />
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
