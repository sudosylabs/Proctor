import {useLocation} from '@docusaurus/router';
import {useColorMode} from '@docusaurus/theme-common';
import OriginalLayout from '@theme-original/Layout';
import type {Props} from '@theme/Layout';
import {useEffect} from 'react';

import styles from './styles.module.css';

function ThemeColor(): null {
  const {colorMode} = useColorMode();

  useEffect(() => {
    const rootStyle = window.getComputedStyle(document.documentElement);
    const navigation = rootStyle.getPropertyValue('--proctor-action').trim();
    let meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
    if (!meta) {
      meta = document.createElement('meta');
      meta.name = 'theme-color';
      document.head.append(meta);
    }
    if (navigation) meta.content = navigation;
  }, [colorMode]);
  return null;
}

export default function Layout({children, ...props}: Props): React.JSX.Element {
  const location = useLocation();
  const isHome = location.pathname === '/';

  return (
    <OriginalLayout {...props}>
      <ThemeColor />
      <div className={`${styles.shell} ${isHome ? styles.home : styles.reader}`}>
        {children}
      </div>
    </OriginalLayout>
  );
}
