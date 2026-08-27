import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";

import {
  themeCatalog,
  type ThemePreference,
} from "../generated/design-system/themes";

export type ProductTheme = (typeof themeCatalog)[number];
export type ProductColorScheme = ProductTheme["colorScheme"];

export interface ThemeColorMetadata {
  content: string;
  media?: string;
}

export interface ResolvedProductTheme {
  effectiveTheme: ProductTheme;
  preference: ThemePreference;
  rootTheme?: ProductTheme["id"];
  themeColorMetadata: ThemeColorMetadata[];
}

const systemColorSchemes = ["light", "dark"] as const;
const ProductThemeContext = createContext<ResolvedProductTheme | undefined>(
  undefined,
);

export function resolveProductTheme(
  preference: ThemePreference,
  systemColorScheme: ProductColorScheme,
): ResolvedProductTheme {
  if (preference === "system") {
    return {
      effectiveTheme: systemTheme(systemColorScheme),
      preference,
      themeColorMetadata: systemColorSchemes.map((colorScheme) => {
        const theme = systemTheme(colorScheme);
        return {
          content: theme.themeColor,
          media: `(prefers-color-scheme: ${colorScheme})`,
        };
      }),
    };
  }

  const effectiveTheme = themeCatalog.find(({ id }) => id === preference);
  if (effectiveTheme === undefined) {
    throw new Error(`Unknown product theme: ${preference}`);
  }
  return {
    effectiveTheme,
    preference,
    rootTheme: effectiveTheme.id,
    themeColorMetadata: [{ content: effectiveTheme.themeColor }],
  };
}

export function readSystemColorScheme(
  media: Pick<Window, "matchMedia"> = window,
): ProductColorScheme {
  return media.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

export function useResolvedProductTheme(
  preference: ThemePreference,
): ResolvedProductTheme {
  const [systemColorScheme, setSystemColorScheme] = useState(() =>
    readSystemColorScheme(),
  );

  useEffect(() => {
    if (preference !== "system") {
      return;
    }
    const query = window.matchMedia("(prefers-color-scheme: dark)");
    const synchronize = () =>
      setSystemColorScheme(query.matches ? "dark" : "light");
    synchronize();
    query.addEventListener("change", synchronize);
    return () => query.removeEventListener("change", synchronize);
  }, [preference]);

  return useMemo(
    () => resolveProductTheme(preference, systemColorScheme),
    [preference, systemColorScheme],
  );
}

export function ProductThemeProvider({
  children,
  value,
}: {
  children: ReactNode;
  value: ResolvedProductTheme;
}) {
  return (
    <ProductThemeContext.Provider value={value}>
      {children}
    </ProductThemeContext.Provider>
  );
}

export function useProductTheme(): ResolvedProductTheme {
  const theme = useContext(ProductThemeContext);
  if (theme === undefined) {
    throw new Error("ProductThemeProvider is missing");
  }
  return theme;
}

function systemTheme(colorScheme: ProductColorScheme): ProductTheme {
  const namedTheme = themeCatalog.find(({ id }) => id === colorScheme);
  const theme =
    namedTheme ?? themeCatalog.find((entry) => entry.colorScheme === colorScheme);
  if (theme === undefined) {
    throw new Error(`No system ${colorScheme} product theme is registered`);
  }
  return theme;
}
