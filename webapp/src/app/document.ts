import {
  themeCatalog,
  type ThemePreference,
} from "../generated/design-system/themes";

export type DocumentDirection = "ltr" | "rtl";

export interface DocumentDescriptor {
  language: string;
  direction: DocumentDirection;
  title: string;
  themePreference: ThemePreference;
}

export const defaultDocumentDescriptor: DocumentDescriptor = {
  language: "en",
  direction: "ltr",
  title: "Proctor",
  themePreference: "system",
};

interface ThemeColorMetadata {
  content: string;
  media?: string;
}

function desiredThemeColorMetadata(
  themePreference: ThemePreference,
): ThemeColorMetadata[] {
  if (themePreference === "system") {
    return themeCatalog.map((theme) => ({
      content: theme.themeColor,
      media: `(prefers-color-scheme: ${theme.colorScheme})`,
    }));
  }

  const theme = themeCatalog.find(({ id }) => id === themePreference);
  if (theme === undefined) {
    return [];
  }
  return [{ content: theme.themeColor }];
}

function themeColorMetadataMatches(
  elements: HTMLMetaElement[],
  desired: ThemeColorMetadata[],
): boolean {
  return (
    elements.length === desired.length &&
    elements.every((element, index) => {
      const metadata = desired[index];
      return (
        metadata !== undefined &&
        element.content === metadata.content &&
        (element.getAttribute("media") ?? undefined) === metadata.media
      );
    })
  );
}

function synchronizeThemeColorMetadata(
  document: Document,
  themePreference: ThemePreference,
) {
  const desired = desiredThemeColorMetadata(themePreference);
  const existing = Array.from(
    document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]'),
  );
  if (themeColorMetadataMatches(existing, desired)) {
    return;
  }

  for (const element of existing) {
    element.remove();
  }
  for (const metadata of desired) {
    const element = document.createElement("meta");
    element.name = "theme-color";
    element.content = metadata.content;
    if (metadata.media !== undefined) {
      element.media = metadata.media;
    }
    document.head.append(element);
  }
}

export function synchronizeDocument(
  document: Document,
  descriptor: DocumentDescriptor,
) {
  const root = document.documentElement;
  root.lang = descriptor.language;
  root.dir = descriptor.direction;
  document.title = descriptor.title;

  if (descriptor.themePreference === "system") {
    root.removeAttribute("data-theme");
  } else {
    root.dataset.theme = descriptor.themePreference;
  }
  synchronizeThemeColorMetadata(document, descriptor.themePreference);
}
