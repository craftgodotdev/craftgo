import { defineConfig } from "vitepress";
import grammar from "./craftgo.tmLanguage.json" with { type: "json" };

// Single source of truth for the published location. The sitemap, canonical
// links, and Open Graph URLs all derive from these, so moving to a custom
// domain is a two-line change here (plus a `docs/public/CNAME` file and the
// repo's Pages domain setting): set SITE_ORIGIN to the domain and BASE to "/".
const SITE_ORIGIN = "https://craftgodotdev.github.io";
const BASE = "/craftgo/";
const SITE_URL = SITE_ORIGIN + BASE; // canonical root, trailing slash included
const SITE_TITLE = "craftgo";
const SITE_DESCRIPTION =
  "Design-first Go framework on net/http. Spec your API, generate everything.";

export default defineConfig({
  title: SITE_TITLE,
  description: SITE_DESCRIPTION,
  // GitHub Pages project site: served under https://craftgodotdev.github.io/craftgo/.
  // The leading+trailing slashes are required; VitePress prefixes every
  // asset URL and internal link with this base, so a wrong/missing base
  // breaks every link on the deployed site.
  base: BASE,
  cleanUrls: true,
  lastUpdated: true,
  ignoreDeadLinks: true,

  // Emit /sitemap.xml so crawlers can discover every page in one fetch;
  // submit this URL in Google Search Console. The hostname includes BASE and
  // VitePress appends each page's path, so entries read as full absolute URLs.
  sitemap: {
    hostname: SITE_URL,
  },

  // Site-wide social-share + crawl metadata. Per-page canonical / og:title /
  // og:description are injected in transformPageData below so each page
  // advertises a unique identity instead of all sharing the site description.
  head: [
    // Google Search Console ownership proof;
    [
      "meta",
      {
        name: "google-site-verification",
        content: "y4FgKl1K8zkkt1qgxlR3UFCWo6R-cXo6fc2sjnb4ghw",
      },
    ],
    ["meta", { property: "og:type", content: "website" }],
    ["meta", { property: "og:site_name", content: SITE_TITLE }],
    ["meta", { name: "twitter:card", content: "summary" }],
  ],

  transformPageData(pageData) {
    const path = pageData.relativePath
      .replace(/index\.md$/, "")
      .replace(/\.md$/, "");
    const canonical = SITE_URL + path;
    const pageTitle = pageData.frontmatter.title || pageData.title;
    const title = pageTitle ? `${pageTitle} | ${SITE_TITLE}` : SITE_TITLE;
    const description = pageData.frontmatter.description || SITE_DESCRIPTION;
    pageData.frontmatter.head ??= [];
    pageData.frontmatter.head.push(
      ["link", { rel: "canonical", href: canonical }],
      ["meta", { property: "og:url", content: canonical }],
      ["meta", { property: "og:title", content: title }],
      ["meta", { property: "og:description", content: description }],
      ["meta", { name: "twitter:title", content: title }],
      ["meta", { name: "twitter:description", content: description }],
    );
  },

  markdown: {
    languages: [
      {
        ...(grammar as any),
        name: "craftgo",
        scopeName: "source.craftgo",
      },
    ],
  },

  themeConfig: {
    nav: [
      { text: "Guide", link: "/guide/getting-started" },
      { text: "Reference", link: "/reference/cli" },
      { text: "Tutorials", link: "/tutorials/todo-api" },
      { text: "AI Reference", link: "/llms" },
      {
        text: "v1.4.2",
        items: [
          {
            text: "Changelog",
            link: "https://github.com/craftgodotdev/craftgo/blob/main/CHANGELOG.md",
          },
          {
            text: "Releases",
            link: "https://github.com/craftgodotdev/craftgo/releases",
          },
        ],
      },
    ],

    sidebar: {
      "/guide/": [
        {
          text: "Introduction",
          items: [
            { text: "Getting Started", link: "/guide/getting-started" },
            { text: "Installation", link: "/guide/installation" },
            { text: "Project Structure", link: "/guide/project-structure" },
          ],
        },
        {
          text: "Core Concepts",
          items: [
            { text: "DSL Basics", link: "/guide/dsl-basics" },
            { text: "Keywords", link: "/guide/keywords" },
            { text: "Why Design-first", link: "/guide/why-design-first" },
            { text: "Runtime", link: "/guide/runtime" },
            { text: "OpenAPI", link: "/guide/openapi" },
            { text: "Validators", link: "/guide/validators" },
            { text: "Performance", link: "/guide/performance" },
          ],
        },
        {
          text: "Types & Decorators",
          items: [
            { text: "Types and Scalars", link: "/guide/types-and-scalars" },
            { text: "Enums", link: "/guide/enums" },
            { text: "Errors", link: "/guide/errors" },
            { text: "Decorators", link: "/guide/decorators" },
          ],
        },
        {
          text: "Application",
          items: [
            { text: "Middleware", link: "/guide/middleware" },
            { text: "Configuration", link: "/guide/configuration" },
            { text: "LSP / IDE", link: "/guide/lsp" },
          ],
        },
      ],
      "/reference/": [
        {
          text: "Reference",
          items: [
            { text: "CLI", link: "/reference/cli" },
            {
              text: "Decorator Registry",
              link: "/reference/decorator-registry",
            },
            { text: "Runtime API", link: "/reference/runtime-api" },
            { text: "Codegen Output", link: "/reference/codegen-output" },
          ],
        },
      ],
      "/tutorials/": [
        {
          text: "Tutorials",
          items: [{ text: "TODO API", link: "/tutorials/todo-api" }],
        },
      ],
    },

    socialLinks: [
      { icon: "github", link: "https://github.com/craftgodotdev/craftgo" },
    ],

    search: {
      provider: "local",
    },

    footer: {
      message: "Released under the MIT License.",
      copyright: "Copyright © 2025-present craftgo authors",
    },

    editLink: {
      pattern: "https://github.com/craftgodotdev/craftgo/edit/main/docs/:path",
      text: "Edit this page on GitHub",
    },
  },
});
