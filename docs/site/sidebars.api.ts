import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';
import referenceSidebar from '../api/reference/sidebar';

const sidebars: SidebarsConfig = {
  apiSidebar: [
    {type: 'doc', id: 'index', label: 'API Overview'},
    {
      type: 'category',
      label: 'Integration Guides',
      collapsed: true,
      items: [
        'guides/getting-started',
        'guides/authentication',
        'guides/errors',
        'guides/pagination',
        'guides/idempotency',
        'guides/uploads-and-content',
        'guides/realtime',
        'guides/durable-operations',
        'guides/limits-and-compatibility',
      ],
    },
    {
      type: 'category',
      label: 'End-to-End Recipes',
      collapsed: true,
      items: [
        'recipes/bootstrap-installation',
        'recipes/account-entry-and-recovery',
        'recipes/account-security',
        'recipes/access-policy',
        'recipes/desktop-authorization',
        'recipes/academic-structure',
        'recipes/roles-and-scopes',
        'recipes/invite-and-link-users',
        'recipes/onboarding-imports-and-progression',
        'recipes/author-and-publish-exam',
        'recipes/manage-sitting',
        'recipes/candidate-attempt',
        'recipes/review-and-release',
        'recipes/consume-audit',
      ],
    },
    {
      type: 'category',
      label: 'Endpoint Reference',
      collapsed: false,
      items: referenceSidebar,
    },
  ],
};

export default sidebars;
