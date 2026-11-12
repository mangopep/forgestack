// @ts-check
// ForgeStack documentation website. Renders the repository's existing
// docs/ directory directly (no copying/duplicating content) so the
// website and docgen's staleness checks are always looking at the same
// files.

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'ForgeStack',
  tagline: 'Go + Docker + AWS + DevSecOps engineering lab',

  // GitHub Pages serves a project (not user/org) page at this path.
  url: 'https://mangopep.github.io',
  baseUrl: '/forgestack/',

  organizationName: 'mangopep',
  projectName: 'forgestack',

  onBrokenLinks: 'throw',
  // Many existing docs weren't written with Docusaurus heading-anchor
  // conventions in mind; warn rather than fail the build on anchor
  // mismatches specifically (actual broken file links still throw above).
  onBrokenAnchors: 'warn',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },
  themes: ['@docusaurus/theme-mermaid'],

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          // Point directly at the repository's real docs/ directory —
          // not a copy. docgen's own staleness checks and this site
          // always read the same source of truth.
          path: '../docs',
          routeBasePath: '/',
          sidebarPath: require.resolve('./sidebars.js'),
          editUrl: 'https://github.com/mangopep/forgestack/edit/main/',
        },
        blog: false,
        theme: {
          customCss: require.resolve('./src/css/custom.css'),
        },
      }),
    ],
  ],

  themeConfig: /** @type {import('@docusaurus/preset-classic').ThemeConfig} */ ({
    navbar: {
      title: 'ForgeStack',
      items: [
        {
          href: 'https://github.com/mangopep/forgestack',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      copyright: `ForgeStack docs — generated from the repository's docs/ directory.`,
    },
    prism: {
      additionalLanguages: ['bash', 'go', 'hcl', 'json', 'yaml'],
    },
  }),
};

module.exports = config;
