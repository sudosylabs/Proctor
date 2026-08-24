import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  guidesSidebar: [
    {type: 'doc', id: 'index', label: 'Overview'},
    {
      type: 'category',
      label: 'Run Proctor',
      collapsed: false,
      items: [{type: 'doc', id: 'operator/index', label: 'Deployment Overview'}],
    },
    {
      type: 'category',
      label: 'Govern One Institution',
      collapsed: false,
      items: [
        {type: 'doc', id: 'institution-admin/index', label: 'Institution Setup'},
        {type: 'doc', id: 'security/index', label: 'Security Review'},
      ],
    },
    {
      type: 'category',
      label: 'Build and Integrate',
      collapsed: false,
      items: [
        {type: 'doc', id: 'developers/index', label: 'Developer Guide'},
        {type: 'link', href: '/api/', label: 'API Reference'},
      ],
    },
    {
      type: 'category',
      label: 'Reference',
      collapsed: false,
      items: [
        {type: 'doc', id: 'reference/glossary', label: 'Glossary'},
        {
          type: 'link',
          href: 'https://github.com/sudosylabs/Proctor/tree/main/docs/architecture',
          label: 'Architecture Source',
        },
      ],
    },
  ],
};

export default sidebars;
