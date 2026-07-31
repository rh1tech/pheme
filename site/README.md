# pheme.rh1.tech — the marketing site

Static HTML, one stylesheet, one small script. **No build step, no framework, no dependencies.**
The files in this directory are the artefact: what is committed is what is served.

That is deliberate. A landing page whose job is to still work in five years should not need a
toolchain that will have moved three major versions by then, and a page that explains end-to-end
encryption should not ship a hundred third-party packages to say so.

## Layout

```
index.html            en landing page
ru/index.html         ru landing page
privacy/  terms/  support/           en legal + help
ru/privacy/  ru/terms/  ru/support/  ru legal + help
404.html              not-found page
robots.txt            crawl policy
sitemap.xml           every URL, with hreflang pairs
assets/css/site.css   the whole design system
assets/js/site.js     mobile menu + reveal-on-scroll
assets/img/           icon and app screenshots (webp)
Dockerfile            nginx image, built and pushed by deploy.yml
nginx.conf            clean URLs, caching, security headers
```

## Editing

Open the file and edit it. To preview:

```sh
cd site && python3 -m http.server 8811
```

Then `http://localhost:8811/`.

### Rules worth keeping

- **Every page exists in both languages**, and both carry the full `hreflang` set pointing at each
  other and at `x-default`. A page that exists in one language only splits its own ranking.
- **`.reveal` hides content until it scrolls into view**, and the class only bites when the inline
  `document.documentElement.classList.add('js')` has run — so with JS disabled the page reads in
  full. Do not put anything essential behind it that is not also in the markup.
- **`.code` blocks are `white-space: pre`** and scroll sideways rather than wrapping. A wrapped
  shell command is a command somebody copies wrongly.
- **Bump the `?v=` on the CSS and JS links** when you change them. The HTML is served
  `no-cache`, the assets are not.
- **The screenshots come from the store package** (`mobile/market/googleplay/screenshots/`),
  converted to webp. Regenerate them there, not here.

## Deployment

Built as `ghcr.io/rh1tech/pheme-site` by `.github/workflows/deploy.yml` on a version tag, and run
as the `site` service in `deploy/prod/docker-compose.yml` on `SITE_HOST_PORT` (8193). The host's
nginx vhost for `pheme.rh1.tech` proxies to it.

**This is not the app.** The app is a separate container on its own domain; nothing here shares
state, storage or a service worker with it. The only link between them is the "Open the app"
button.
