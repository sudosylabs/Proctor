import { useEffect } from "react";

import { message } from "../i18n/messages";
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
}

export function App({ bootstrap }: AppProps) {
  const title = message(hostedRouteDocumentTitle(bootstrap.route));

  useEffect(() => {
    synchronizeDocument(document, {
      ...defaultDocumentDescriptor,
      title,
    });
  }, [title]);

  return renderHostedPage(bootstrap);
}
