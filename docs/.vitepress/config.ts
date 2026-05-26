import { defineConfig } from "vitepress";
import grammar from "./craftgo.tmLanguage.json" with { type: "json" };

export default defineConfig({
  title: "craftgo",
  description:
    "Design-first Go framework on net/http. Spec your API, generate everything.",
  // GitHub Pages project site: served under https://craftgodotdev.github.io/craftgo/.
  // The leading+trailing slashes are required; VitePress prefixes every
  // asset URL and internal link with this base, so a wrong/missing base
  // breaks every link on the deployed site.
  base: "/craftgo/",
  cleanUrls: true,
  lastUpdated: true,
  ignoreDeadLinks: true,

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
        text: "v0.x",
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
