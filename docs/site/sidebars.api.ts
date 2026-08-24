import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';
import referenceSidebar from '../api/reference/sidebar';

const sidebars: SidebarsConfig = {
  apiSidebar: [
    {type: 'doc', id: 'index', label: 'API Overview'},
    {
      type: 'category',
      label: 'Endpoint Reference',
      collapsed: false,
      items: referenceSidebar,
    },
  ],
};

export default sidebars;
