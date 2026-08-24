import Head from '@docusaurus/Head';
import {useColorMode} from '@docusaurus/theme-common';
import OriginalLayout from '@theme-original/Layout';
import type {Props} from '@theme/Layout';

function ThemeColor(): React.JSX.Element {
  const {colorMode} = useColorMode();

  return (
    <Head>
      <meta
        content={colorMode === 'dark' ? '#11141c' : '#ffffff'}
        name="theme-color"
      />
    </Head>
  );
}

export default function Layout({children, ...props}: Props): React.JSX.Element {
  return (
    <OriginalLayout {...props}>
      <ThemeColor />
      {children}
    </OriginalLayout>
  );
}
