import React from 'react';

import assetRegistry from '../../../../public/assets.json';
import styles from './styles.module.css';

type AssetEntry = (typeof assetRegistry.assets)[number];

type GovernedFigureProps = {
  asset: string;
  eager?: boolean;
};

const assetsByID = new Map<string, AssetEntry>(
  assetRegistry.assets.map((entry) => [entry.id, entry]),
);

export default function GovernedFigure({
  asset,
  eager = false,
}: GovernedFigureProps): React.ReactElement {
  const entry = assetsByID.get(asset);

  if (!entry) {
    throw new Error(`Unknown governed documentation asset: ${asset}`);
  }

  return (
    <figure className={styles.figure} data-asset-id={entry.id}>
      <div className={styles.plate}>
        <img
          alt={entry.alt}
          className={styles.image}
          decoding="async"
          fetchPriority={eager ? 'high' : undefined}
          height={entry.height}
          loading={eager ? 'eager' : 'lazy'}
          src={entry.public_path}
          width={entry.width}
        />
      </div>
      <figcaption className={styles.caption}>
        <span className={styles.kind}>{entry.kind}</span>
        <span>{entry.caption}</span>
        <a
          aria-label={`Open ${entry.kind} at full size in a new tab`}
          className={styles.fullSize}
          href={entry.public_path}
          rel="noopener noreferrer"
          target="_blank">
          Open full size <span aria-hidden="true">↗</span>
        </a>
      </figcaption>
    </figure>
  );
}
