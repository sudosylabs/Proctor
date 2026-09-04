import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';
import {guides, referenceLinks} from './navigation.mjs';

const config: Config = {
  title: 'Proctor Documentation',
  tagline: 'Run examinations with the rules in view',
  favicon: 'img/brand/proctor-mark-dark.svg',

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
          customCss: [
            './src/css/tokens.css',
            './src/css/custom.css',
            './src/css/api.css',
          ],
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
      {language: 'curl', variant: 'cURL', variants: ['cURL']},
      {language: 'javascript', variant: 'Fetch', variants: ['Fetch']},
      {language: 'python', variant: 'Requests', variants: ['Requests']},
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
      logo: {
        alt: 'Proctor Docs',
        src: 'img/brand/proctor-docs-lockup-white.svg',
        srcDark: 'img/brand/proctor-docs-lockup-white.svg',
        width: 216,
        height: 28,
      },
      items: [
        {
          type: 'dropdown',
          label: 'Guides',
          position: 'left',
          items: guides.map(({label, sidebarId}) => ({
            type: 'docSidebar',
            label,
            sidebarId,
          })),
        },
        {
          type: 'docSidebar',
          sidebarId: 'developerSidebar',
          label: 'Developers',
          position: 'left',
        },
        {to: '/api/', label: 'API reference', position: 'left'},
        {
          type: 'dropdown',
          label: 'Reference',
          position: 'left',
          items: referenceLinks,
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
      logo: {
        alt: 'Proctor Docs',
        src: 'img/brand/proctor-docs-lockup-white.svg',
        srcDark: 'img/brand/proctor-docs-lockup-white.svg',
        width: 192,
        height: 25,
      },
      links: [
        {
          title: 'Use Proctor',
          items: [
            {label: 'Your account', to: '/account/'},
            {label: 'Take an exam', to: '/candidate/'},
            {label: 'Manage exams', to: '/exam-manager/'},
          ],
        },
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
            {label: 'Glossary', to: '/glossary/'},
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
      theme: prismThemes.dracula,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'json', 'go'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
