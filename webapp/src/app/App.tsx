import { useLayoutEffect } from "react";

import type { ThemePreference } from "../generated/design-system/themes";
import { message } from "../i18n/messages";
import {
  ProductThemeProvider,
  useResolvedProductTheme,
} from "../theme/ProductTheme";
import {
  defaultDocumentDescriptor,
  synchronizeDocument,
} from "./document";
import {
  hostedRouteDocumentTitle,
  renderHostedPage,
  type HostedPageBootstrap,
} from "./HostedRoutes";

export interface AppProps {
  bootstrap: HostedPageBootstrap;
  themePreference?: ThemePreference;
}

export function App({ bootstrap, themePreference = "system" }: AppProps) {
  const title = message(hostedRouteDocumentTitle(bootstrap.route));
  const theme = useResolvedProductTheme(themePreference);

  useLayoutEffect(() => {
    synchronizeDocument(
      document,
      {
        ...defaultDocumentDescriptor,
        title,
      },
      theme,
    );
  }, [theme, title]);

  return (
    <ProductThemeProvider value={theme}>
      {renderHostedPage(bootstrap)}
    </ProductThemeProvider>
  );
}
