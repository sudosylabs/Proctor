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
              id: 'developers/tooling-and-debugging',
              label: 'Tooling and Debugging',
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
                  type: 'category',
                  label: 'Server Development',
                  collapsed: false,
                  link: {
                    type: 'doc',
                    id: 'developers/server-workflow',
                  },
                  items: [
                    {
                      type: 'doc',
                      id: 'developers/server/architecture-composition',
                      label: 'Architecture and Composition',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/domain-application',
                      label: 'Domain and Application',
                    },
                    {
                      type: 'doc',
                      id: 'developers/persistence',
                      label: 'Persistence and Transactions',
                    },
                    {
                      type: 'doc',
                      id: 'developers/http-and-openapi',
                      label: 'HTTP and OpenAPI',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/authorization-errors-audit',
                      label: 'Authorization, Errors, and Audit',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/jobs-mail',
                      label: 'Jobs and Transactional Mail',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/files-execution',
                      label: 'Files, Workspaces, and Execution',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/realtime-cluster-effects',
                      label: 'Realtime, Cluster, and Effects',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/runtime-integrations',
                      label: 'Runtime and Integrations',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/user-settings-slice',
                      label: 'Complete Vertical Slice',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/review-checklists',
                      label: 'Review Checklists',
                    },
                  ],
                },
                {
                  type: 'category',
                  label: 'Webapp Development',
                  collapsed: false,
                  link: {
                    type: 'doc',
                    id: 'developers/webapp-workflow',
                  },
                  items: [
                    {
                      type: 'doc',
                      id: 'developers/webapp/architecture-routing',
                      label: 'Architecture and Routing',
                    },
                    {
                      type: 'doc',
                      id: 'developers/webapp/features-api-localization',
                      label: 'Features, API, and Localization',
                    },
                    {
                      type: 'doc',
                      id: 'developers/webapp/states-forms-accessibility',
                      label: 'States, Forms, and Accessibility',
                    },
                    {
                      type: 'doc',
                      id: 'developers/webapp/testing',
                      label: 'Testing the Webapp',
                    },
                    {
                      type: 'doc',
                      id: 'developers/webapp/visual-review',
                      label: 'Visual Review',
                    },
                    {
                      type: 'doc',
                      id: 'developers/webapp/password-reset-slice',
                      label: 'Complete Vertical Slice',
                    },
                    {
                      type: 'doc',
                      id: 'developers/webapp/review-checklists',
                      label: 'Review Checklists',
                    },
                  ],
                },
                {
                  type: 'doc',
                  id: 'developers/reusable-modules',
                  label: 'Reusable Modules',
                },
              ],
            },
            {
              type: 'doc',
              id: 'developers/testing',
              label: 'Testing and Verification',
            },
            {
              type: 'category',
              label: 'Troubleshooting',
              collapsed: true,
              link: {
                type: 'doc',
                id: 'developers/troubleshooting/index',
              },
              items: [
                {
                  type: 'doc',
                  id: 'developers/troubleshooting/runtime-readiness',
                  label: 'Runtime and Readiness',
                },
                {
                  type: 'doc',
                  id: 'developers/troubleshooting/http-access',
                  label: 'HTTP Access',
                },
                {
                  type: 'doc',
                  id: 'developers/troubleshooting/data-content',
                  label: 'Data and Content',
                },
                {
                  type: 'doc',
                  id: 'developers/troubleshooting/mail',
                  label: 'Transactional Mail',
                },
                {
                  type: 'doc',
                  id: 'developers/troubleshooting/jobs-realtime',
                  label: 'Jobs and Realtime',
                },
                {
                  type: 'doc',
                  id: 'developers/troubleshooting/browser',
                  label: 'Browser Application',
                },
                {
                  type: 'doc',
                  id: 'developers/troubleshooting/cluster',
                  label: 'Cluster Failures',
                },
              ],
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
