import styles from './styles.module.css';

const stages = [
  {name: 'Exam', detail: 'Long-lived academic identity'},
  {name: 'Draft', detail: 'Mutable working state'},
  {name: 'Revision', detail: 'Immutable publication'},
  {name: 'Sitting', detail: 'Scheduled delivery'},
  {name: 'Attempt', detail: 'Candidate participation'},
];

export default function LifecycleRibbon(): React.JSX.Element {
  return (
    <section className={styles.wrapper} aria-labelledby="lifecycle-title">
      <div className={styles.intro}>
        <p className={styles.eyebrow}>Examination lifecycle</p>
        <h2 id="lifecycle-title">Follow the Domain, Not the Screen</h2>
        <p>
          Proctor keeps authoring, publication, scheduling, and participation
          separate. Guides and reference material follow the same boundaries.
        </p>
      </div>
      <ol className={styles.stages} aria-label="Examination lifecycle">
        {stages.map((stage, index) => (
          <li className={styles.stage} key={stage.name}>
            <span className={styles.index} aria-hidden="true">{index + 1}</span>
            <strong>{stage.name}</strong>
            <span>{stage.detail}</span>
          </li>
        ))}
      </ol>
    </section>
  );
}
