import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'Proctor Documentation',
  tagline: 'Run examinations with the rules in view',

  // Keep authored and generated content on Docusaurus' forward-compatible MDX
  // parser. The API plugin has its own content root so every file is compiled
  // exactly once.
  future: {
    v4: true,
  },

  // Publication is intentionally deferred. The placeholder prevents a local
  // build from inventing a production hostname before that decision is made.
  url: process.env.DOCS_SITE_URL ?? 'https://docs.proctor.invalid',
  baseUrl: process.env.BASE_URL ?? '/',
  staticDirectories: ['static', '../public/static'],
  trailingSlash: false,

  organizationName: 'sudosylabs',
  projectName: 'Proctor',

  onBrokenLinks: 'throw',
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'throw',
      onBrokenMarkdownImages: 'throw',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          path: '../public',
          routeBasePath: '/',
          sidebarPath: './sidebars.ts',
          editUrl: ({docPath}) =>
            `https://github.com/sudosylabs/Proctor/edit/main/docs/public/${docPath}`,
          breadcrumbs: true,
        },
        blog: false,
        theme: {
          customCss: ['./src/css/tokens.css', './src/css/custom.css'],
        },
      } satisfies Preset.Options,
    ],
  ],

  plugins: [
    [
      '@docusaurus/plugin-content-docs',
      {
        id: 'api',
        path: '../api',
        routeBasePath: '/api',
        sidebarPath: './sidebars.api.ts',
        docItemComponent: '@theme/ApiItem',
        editUrl: ({docPath}: {docPath: string}) =>
          `https://github.com/sudosylabs/Proctor/edit/main/docs/api/${docPath}`,
        breadcrumbs: true,
      },
    ],
    [
      'docusaurus-plugin-openapi-docs',
      {
        id: 'api-generator',
        docsPluginId: 'api',
        config: {
          proctor: {
            specPath: '../../server/openapi.json',
            outputDir: '../api/reference',
            template: './templates/api.mdx.mustache',
            downloadUrl: '/openapi/openapi.json',
            hideSendButton: true,
            showInfoPage: false,
            showSchemas: false,
            disableCompression: true,
            externalJsonProps: true,
            sidebarOptions: {
              groupPathsBy: 'tag',
              categoryLinkSource: 'tag',
            },
          },
        },
      },
    ],
  ],

  themes: ['docusaurus-theme-openapi-docs'],

  themeConfig: {
    // Keep the generated examples focused on the three client paths we test.
    languageTabs: [
      {language: 'curl', variant: 'cURL'},
      {language: 'javascript', variant: 'Fetch'},
      {language: 'python', variant: 'Requests'},
    ],
    metadata: [
      {
        name: 'keywords',
        content: 'Proctor, examination, self-hosted, documentation, API',
      },
    ],
    colorMode: {
      defaultMode: 'light',
      respectPrefersColorScheme: false,
    },
    navbar: {
      title: 'Proctor Docs',
      items: [
        {to: '/', label: 'Start', position: 'left', exact: true},
        {to: '/operator/', label: 'Operate', position: 'left'},
        {
          to: '/institution-admin/',
          label: 'Administer',
          position: 'left',
        },
        {to: '/security/', label: 'Secure', position: 'left'},
        {
          type: 'dropdown',
          label: 'Build',
          position: 'left',
          items: [
            {to: '/developers/', label: 'Developer Guide'},
            {to: '/api/', label: 'API Reference'},
          ],
        },
        {
          href: 'https://github.com/sudosylabs/Proctor',
          label: 'GitHub',
          position: 'right',
        },
        {type: 'search', position: 'right'},
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Run Proctor',
          items: [
            {label: 'Deployment Guide', to: '/operator/'},
            {label: 'Institution Setup', to: '/institution-admin/'},
            {label: 'Security Review', to: '/security/'},
          ],
        },
        {
          title: 'Build With Proctor',
          items: [
            {label: 'Developer Guide', to: '/developers/'},
            {label: 'API Reference', to: '/api/'},
            {
              label: 'Source Repository',
              href: 'https://github.com/sudosylabs/Proctor',
            },
          ],
        },
      ],
      copyright: 'Proctor is open-source examination infrastructure.',
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'json', 'go'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
