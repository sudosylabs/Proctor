import { catalogs } from "../generated/i18n/catalogs";

export type MessageID = keyof typeof catalogs.en;

export function message(
  id: MessageID,
  values: Readonly<Record<string, string>> = {},
): string {
  let translation: string = catalogs.en[id];
  for (const [name, value] of Object.entries(values)) {
    translation = translation.replaceAll(`{{.${name}}}`, value);
  }
  return translation;
}
