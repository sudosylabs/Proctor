// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Generated from design-system/tokens.mjs. Do not edit.
export const themeCatalog = [
  {
    "id": "light",
    "colorScheme": "light",
    "themeColor": "#ffffff"
  },
  {
    "id": "dark",
    "colorScheme": "dark",
    "themeColor": "#141016"
  }
] as const;

export type ThemeID = (typeof themeCatalog)[number]["id"];

export const themePreferenceValues = [
  "system",
  ...themeCatalog.map((theme) => theme.id),
] as const;

export type ThemePreference = (typeof themePreferenceValues)[number];

export function isThemeID(value: string): value is ThemeID {
  return themeCatalog.some((theme) => theme.id === value);
}

export function isThemePreference(value: string): value is ThemePreference {
  return value === "system" || isThemeID(value);
}
