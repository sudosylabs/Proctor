import OriginalCodeTabs from '@theme-original/ApiExplorer/CodeTabs';
import type {TabsProps} from '@docusaurus/theme-common/internal';
import type {CodeTabsProps} from 'docusaurus-theme-openapi-docs/lib/theme/ApiExplorer/CodeTabs';
import {
  Children,
  cloneElement,
  isValidElement,
  useEffect,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from 'react';

// The API theme's declaration omits Docusaurus' current tab props.
type Props = CodeTabsProps & TabsProps;
type Language = Props['languageSet'][number];

function ActiveLanguage({language, setLanguage}: {
  language: Language;
  setLanguage: Dispatch<SetStateAction<Language>>;
}): null {
  useEffect(() => {
    setLanguage((current) =>
      current.language === language.language ? current : language,
    );
  }, [language, setLanguage]);
  return null;
}

export default function CodeTabs(props: Props): React.JSX.Element {
  if (props.groupId !== 'code-samples' || !props.lazy) {
    return <OriginalCodeTabs {...props} />;
  }

  // Docusaurus restores the visible tab from namespaced storage, but the API
  // theme initializes its generator from an unnamespaced key. Synchronize from
  // the mounted (lazy) panel so its label and generated language cannot differ.
  return (
    <OriginalCodeTabs {...props}>
      {Children.map(props.children, (child) => {
        if (!isValidElement<{value: string; children: ReactNode}>(child)) {
          return child;
        }
        const language = props.languageSet.find(
          (item) => item.language === child.props.value,
        );
        if (!language) return child;
        return cloneElement(child, {}, (
          <>
            <ActiveLanguage language={language} setLanguage={props.action.setLanguage} />
            {child.props.children}
          </>
        ));
      })}
    </OriginalCodeTabs>
  );
}
