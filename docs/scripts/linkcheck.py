#!/usr/bin/env python3
"""Check that every internal link and image in the built docs site resolves.

`ignoreDeadLinks` is on in the VitePress config, so a broken internal link is
not a build failure - this is the gate that catches it. Build first, then run:

    cd docs && npm run build && python3 scripts/linkcheck.py

It walks docs/.vitepress/dist, collects every href/src that points inside the
site, and verifies the target page (including its #anchor id) or asset exists.
Exits non-zero and lists every failure.
"""

import html
import os
import re
import sys
from urllib.parse import unquote, urlsplit

DOCS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DIST = os.path.join(DOCS, ".vitepress", "dist")
BASE = "/craftgo/"
ASSET_SUFFIXES = re.compile(
    r"\.(svg|png|jpe?g|gif|webp|ico|css|js|json|woff2?|xml|txt|ya?ml)$", re.I
)
EXTERNAL = ("http://", "https://", "mailto:", "data:", "#", "javascript:")


def load_pages():
    pages = {}
    for root, _, files in os.walk(DIST):
        for name in files:
            if name.endswith(".html"):
                path = os.path.join(root, name)
                with open(path, encoding="utf-8") as fh:
                    pages[path] = fh.read()
    return pages


def resolve(path):
    """Map a site path (base already stripped) onto a file under dist."""
    rel = path.strip("/")
    if not rel:
        return os.path.join(DIST, "index.html")
    for candidate in (
        os.path.join(DIST, rel),
        os.path.join(DIST, rel + ".html"),
        os.path.join(DIST, rel, "index.html"),
    ):
        if os.path.isfile(candidate):
            return candidate
    return None


def main():
    if not os.path.isdir(DIST):
        sys.exit(f"no build at {DIST} - run `npm run build` in docs/ first")

    pages = load_pages()
    ids = {path: set(re.findall(r'\bid="([^"]+)"', text)) for path, text in pages.items()}
    broken, checked = [], 0

    for page, text in sorted(pages.items()):
        src = os.path.relpath(page, DIST)
        for attr, raw in re.findall(r'\b(href|src)="([^"]+)"', text):
            url = html.unescape(raw)
            if url.startswith(EXTERNAL) or not url.startswith("/"):
                continue
            if not url.startswith(BASE):
                broken.append(f"{src}: {attr}={url} is not under the site base {BASE}")
                continue
            parts = urlsplit(url)
            path = unquote(parts.path[len(BASE) - 1 :])
            checked += 1
            if ASSET_SUFFIXES.search(path):
                if not os.path.isfile(os.path.join(DIST, path.strip("/"))):
                    broken.append(f"{src}: asset {url} is missing")
                continue
            target = resolve(path)
            if target is None:
                broken.append(f"{src}: page {url} is missing")
            elif parts.fragment and parts.fragment not in ids[target]:
                broken.append(
                    f"{src}: anchor #{parts.fragment} is missing in "
                    f"{os.path.relpath(target, DIST)}"
                )

    print(f"checked {checked} internal links and assets across {len(pages)} pages")
    if broken:
        print(f"{len(broken)} broken:")
        for line in sorted(set(broken)):
            print("  " + line)
        return 1
    print("every internal link and image resolves")
    return 0


if __name__ == "__main__":
    sys.exit(main())
