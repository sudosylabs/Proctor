import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  guidesSidebar: [
    {type: 'doc', id: 'index', label: 'Documentation Home'},
    {
      type: 'category',
      label: 'Operate',
      collapsed: false,
      items: [{type: 'doc', id: 'operator/index', label: 'Deployment Overview'}],
    },
    {
      type: 'category',
      label: 'Administer',
      collapsed: false,
      items: [
        {type: 'doc', id: 'institution-admin/index', label: 'Institution Setup'},
      ],
    },
    {
      type: 'category',
      label: 'Review & Secure',
      collapsed: false,
      items: [{type: 'doc', id: 'security/index', label: 'Security Overview'}],
    },
    {
      type: 'category',
      label: 'Build & Integrate',
      collapsed: false,
      items: [
        {type: 'doc', id: 'developers/index', label: 'Developer Guide'},
        {type: 'doc', id: 'api/index', label: 'API Reference'},
      ],
    },
  ],
};

export default sidebars;
