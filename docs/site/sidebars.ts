import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  guidesSidebar: [
    {type: 'doc', id: 'index', label: 'Overview'},
    {
      type: 'category',
      label: 'Use Your Account',
      collapsed: false,
      link: {type: 'doc', id: 'account/index'},
      items: [
        {type: 'doc', id: 'account/access', label: 'Sign In and Recover Access'},
        {type: 'doc', id: 'account/security', label: 'Secure Your Account'},
        {type: 'doc', id: 'account/desktop-authorization', label: 'Authorize Proctor Desktop'},
      ],
    },
    {
      type: 'category',
      label: 'Take an Exam',
      collapsed: false,
      link: {type: 'doc', id: 'candidate/index'},
      items: [
        {type: 'doc', id: 'candidate/prepare-and-enter', label: 'Prepare and Enter'},
        {type: 'doc', id: 'candidate/workspace-and-continuity', label: 'Workspace and Continuity'},
        {type: 'doc', id: 'candidate/browser-and-privacy', label: 'Browser and Privacy'},
        {type: 'doc', id: 'candidate/submit-and-recover', label: 'Submit and Recover'},
        {type: 'doc', id: 'candidate/released-result', label: 'Released Result'},
      ],
    },
    {
      type: 'category',
      label: 'Run Proctor',
      collapsed: false,
      link: {type: 'doc', id: 'operator/index'},
      items: [
        {type: 'doc', id: 'operator/package-and-install', label: 'Package and Install'},
        {type: 'doc', id: 'operator/configuration-and-secrets', label: 'Configuration and Secrets'},
        {type: 'doc', id: 'operator/systemd-and-container', label: 'Systemd and Container'},
        {type: 'doc', id: 'operator/topology-and-dependencies', label: 'Topology and Dependencies'},
        {type: 'doc', id: 'operator/tls-and-proxy', label: 'TLS and Proxy'},
        {type: 'doc', id: 'operator/health-and-observability', label: 'Health and Observability'},
        {type: 'doc', id: 'operator/maintenance-and-recovery', label: 'Maintenance and Recovery'},
        {type: 'doc', id: 'operator/pre-production-readiness', label: 'Pre-production Readiness'},
      ],
    },
    {
      type: 'category',
      label: 'Govern One Institution',
      collapsed: false,
      items: [
        {type: 'doc', id: 'institution-admin/index', label: 'Institution Setup'},
        {type: 'doc', id: 'institution-admin/access-policy', label: 'Access Policy'},
        {type: 'doc', id: 'institution-admin/academic-structure', label: 'Academic Structure'},
        {type: 'doc', id: 'institution-admin/people-and-authority', label: 'People and Scoped Authority'},
        {type: 'doc', id: 'institution-admin/onboarding', label: 'Invitations and Onboarding'},
        {type: 'doc', id: 'institution-admin/imports-and-progression', label: 'Imports and Progression'},
        {type: 'doc', id: 'institution-admin/audit-history', label: 'Audit History'},
      ],
    },
    {
      type: 'category',
      label: 'Manage Exams',
      collapsed: false,
      link: {type: 'doc', id: 'exam-manager/index'},
      items: [
        {type: 'doc', id: 'exam-manager/author-and-publish', label: 'Author and Publish'},
        {type: 'doc', id: 'exam-manager/resources-and-workspace', label: 'Resources and Starter Workspace'},
        {type: 'doc', id: 'exam-manager/execution-profile', label: 'Execution Profile'},
        {type: 'doc', id: 'exam-manager/sitting-operations', label: 'Sitting Operations'},
        {type: 'doc', id: 'exam-manager/corrections-and-attempt-control', label: 'Corrections and Attempt Control'},
        {type: 'doc', id: 'exam-manager/submission-and-integrity-review', label: 'Submission and Integrity Review'},
        {type: 'doc', id: 'exam-manager/finalize-and-release', label: 'Finalize and Release'},
      ],
    },
    {
      type: 'category',
      label: 'Review Security',
      collapsed: false,
      link: {type: 'doc', id: 'security/index'},
      items: [
        {type: 'doc', id: 'security/threat-model', label: 'Threat Model and Trust Boundaries'},
        {type: 'doc', id: 'security/credentials-authorization', label: 'Credentials and Authorization'},
        {type: 'doc', id: 'security/data-privacy', label: 'Data and Privacy'},
        {type: 'doc', id: 'security/keys-network', label: 'Keys, Certificates, and Network'},
        {type: 'doc', id: 'security/content-execution', label: 'Content and Execution'},
        {type: 'doc', id: 'security/errors-limits-compatibility', label: 'Errors, Limits, and Compatibility'},
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
                      id: 'developers/server/desktop-trust-admission',
                      label: 'Desktop Trust and Admission',
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
                      id: 'developers/server/attempt-continuity',
                      label: 'Attempt Continuity',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/browser-activity-submission',
                      label: 'Browser Activity and Submission',
                    },
                    {
                      type: 'doc',
                      id: 'developers/server/integrity-review-release',
                      label: 'Integrity Review and Release',
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
        {type: 'doc', id: 'reference/configuration', label: 'Configuration'},
        {type: 'doc', id: 'reference/environment-variables', label: 'Environment Variables'},
        {type: 'doc', id: 'reference/command-line', label: 'Command Line'},
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
