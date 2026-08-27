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
        {
          type: 'category',
          label: 'Developer Guide',
          collapsed: false,
          link: {type: 'doc', id: 'developers/index'},
          items: [
            {type: 'doc', id: 'developers/local-setup', label: 'Local Setup'},
            {
              type: 'doc',
              id: 'developers/local-services',
              label: 'Local Services',
            },
            {
              type: 'doc',
              id: 'developers/cluster-development',
              label: 'Cluster Development',
            },
            {
              type: 'doc',
              id: 'developers/development-configuration',
              label: 'Development Configuration',
            },
            {
              type: 'doc',
              id: 'developers/repository-boundaries',
              label: 'Repository Boundaries',
            },
            {
              type: 'category',
              label: 'Work on Proctor',
              collapsed: true,
              items: [
                {
                  type: 'doc',
                  id: 'developers/server-workflow',
                  label: 'Server Workflow',
                },
                {
                  type: 'doc',
                  id: 'developers/webapp-workflow',
                  label: 'Webapp Workflow',
                },
                {
                  type: 'doc',
                  id: 'developers/reusable-modules',
                  label: 'Reusable Modules',
                },
                {
                  type: 'doc',
                  id: 'developers/http-and-openapi',
                  label: 'HTTP and OpenAPI',
                },
                {
                  type: 'doc',
                  id: 'developers/persistence',
                  label: 'Persistence',
                },
              ],
            },
            {
              type: 'doc',
              id: 'developers/testing',
              label: 'Testing and Verification',
            },
            {
              type: 'doc',
              id: 'developers/documentation',
              label: 'Documentation',
            },
            {
              type: 'doc',
              id: 'developers/contribution-checklist',
              label: 'Contribution Checklist',
            },
          ],
        },
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
