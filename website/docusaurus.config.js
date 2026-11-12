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
  themes: [
    '@docusaurus/theme-mermaid',
    // Free, build-time-indexed local search — no Algolia account, no paid
    // SaaS, no runtime network call. Index is generated at `npm run build`
    // time and served as static JSON alongside the rest of the site.
    [
      '@easyops-cn/docusaurus-search-local',
      /** @type {import('@easyops-cn/docusaurus-search-local').PluginOptions} */
      ({
        hashed: true,
        language: ['en'],
        indexDocs: true,
        indexPages: false,
        docsRouteBasePath: '/',
      }),
    ],
  ],

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
      logo: {
        // static/img/forgestack-banner.svg is a tracked copy of
        // docs/assets/forgestack-banner.svg (Docusaurus's navbar logo must
        // resolve under static/). A symlink was tried first but rejected —
        // Windows clones without core.symlinks enabled would get a broken
        // logo (a text file containing the link target, not the image).
        // Keep these two files identical by hand if the banner changes;
        // see docs/handbook/DOCS-DEVELOPMENT.md and
        // docs/adr/0003-documentation-as-code.md.
        alt: 'ForgeStack',
        src: 'img/forgestack-banner.svg',
      },
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
