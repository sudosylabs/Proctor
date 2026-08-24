import type {SVGProps} from 'react';

import styles from './styles.module.css';

export const DOC_ICON_NAMES = [
  'deployment',
  'institution',
  'assurance',
  'integration',
  'api',
  'glossary',
  'architecture',
  'search',
  'close',
  'arrowRight',
  'external',
  'check',
] as const;

export type DocIconName = (typeof DOC_ICON_NAMES)[number];
export type DocIconSize = 24 | 32 | 48;

const geometry: Record<DocIconName, React.ReactNode> = {
  deployment: (
    <>
      <rect height="6" rx="2" width="18" x="3" y="3" />
      <rect height="6" rx="2" width="18" x="3" y="15" />
      <path d="M7 6h.01M7 18h.01M11 6h6M11 18h6" />
    </>
  ),
  institution: (
    <>
      <path d="m3 9 9-5 9 5" />
      <path d="M5 10v8M9.5 10v8M14.5 10v8M19 10v8M3 20h18" />
    </>
  ),
  assurance: (
    <>
      <path d="M12 3 20 6v5c0 5.2-3.1 8.2-8 10-4.9-1.8-8-4.8-8-10V6l8-3Z" />
      <path d="m8.5 12 2.2 2.2 4.8-5" />
    </>
  ),
  integration: (
    <>
      <path d="m9 7-5 5 5 5M15 7l5 5-5 5M13 4l-2 16" />
    </>
  ),
  api: (
    <>
      <rect height="6" rx="2" width="8" x="2" y="3" />
      <rect height="6" rx="2" width="8" x="14" y="15" />
      <path d="M10 6h3a3 3 0 0 1 3 3v6M14 12l2 3 3-2" />
    </>
  ),
  glossary: (
    <>
      <path d="M4 4h6a3 3 0 0 1 3 3v13a3 3 0 0 0-3-3H4V4Z" />
      <path d="M20 4h-4a3 3 0 0 0-3 3v13a3 3 0 0 1 3-3h4V4Z" />
    </>
  ),
  architecture: (
    <>
      <rect height="6" rx="2" width="8" x="8" y="3" />
      <rect height="6" rx="2" width="8" x="2" y="15" />
      <rect height="6" rx="2" width="8" x="14" y="15" />
      <path d="M12 9v3M6 15v-3h12v3" />
    </>
  ),
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6.5" />
      <path d="m15.5 15.5 4.5 4.5" />
    </>
  ),
  close: <path d="m5 5 14 14M19 5 5 19" />,
  arrowRight: <path d="M4 12h15M14 7l5 5-5 5" />,
  external: (
    <>
      <path d="M13 5h6v6M11 13l8-8" />
      <path d="M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4" />
    </>
  ),
  check: <path d="m5 12 4 4L19 6" />,
};

type Props = Omit<SVGProps<SVGSVGElement>, 'name'> & {
  name: DocIconName;
  size?: DocIconSize;
  title?: string;
};

export default function DocIcon({
  className,
  name,
  size = 24,
  title,
  ...props
}: Props): React.JSX.Element {
  const accessible = Boolean(title);

  return (
    <svg
      {...props}
      aria-hidden={accessible ? undefined : true}
      aria-label={accessible ? title : undefined}
      className={[styles.root, className].filter(Boolean).join(' ')}
      fill="none"
      focusable="false"
      height={size}
      role={accessible ? 'img' : undefined}
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="2"
      viewBox="0 0 24 24"
      width={size}>
      {accessible ? <title>{title}</title> : null}
      {geometry[name]}
    </svg>
  );
}
