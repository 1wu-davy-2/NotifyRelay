import { writeFileSync } from 'node:fs'

import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

/**
 * Puts the embed placeholder back after every build.
 *
 * `//go:embed all:dist` fails to compile when the pattern matches nothing —
 * "no matching files found" — and dist/ is a build artefact that is not
 * committed. The .gitkeep is what a checkout that has never run npm has for the
 * pattern to match, so that `go build ./...` works for somebody working on the
 * backend.
 *
 * emptyOutDir deletes it along with everything else, which would leave the
 * repository showing a deleted file after every build and — if that deletion
 * were ever committed — a clone whose `go build` fails outright. Recreating it
 * here means the file is simply always present.
 *
 * A Vite plugin rather than a second npm script, because a script is a thing
 * somebody has to remember to chain and this is not.
 */
function keepEmbedPlaceholder() {
  return {
    name: 'notifyrelay:keep-embed-placeholder',
    closeBundle() {
      writeFileSync(
        new URL('./dist/.gitkeep', import.meta.url),
        'Placeholder so //go:embed all:dist matches something in a checkout where\n' +
          'npm run build has not been run. See web/embed.go and internal/admin/spa.go.\n' +
          'Recreated by the keepEmbedPlaceholder plugin in vite.config.ts.\n',
      )
    },
  }
}

// The admin interface is served from the same Go process that answers its API,
// so the build has to produce something that process can hand out byte for byte.
//
// Two settings here are not defaults, and both are consequences of the CSP the
// admin sets (internal/admin/admin.go): `script-src 'self'; style-src 'self'`,
// with no 'unsafe-inline'. A build that inlined its script or its styles would
// be blocked by the very page it was built for — and blocked quietly, as a
// console error nobody reads until the interface is blank.
//
//   modulePreload.polyfill: false   the polyfill is injected as an inline
//                                   <script> in some targets. Module preload is
//                                   supported by every browser that can run the
//                                   rest of this, so the polyfill buys nothing
//                                   and costs the CSP.
//
//   cssCodeSplit / assetsDir        default behaviour already writes CSS to a
//                                   file rather than a <style> block. Named
//                                   explicitly so that a future change to the
//                                   defaults cannot silently reintroduce one.
//
// `npm run build` output is checked into neither git nor the binary: dist/ is
// embedded by web/embed.go at compile time, and a build that has not run leaves
// the admin serving a page that says so rather than a blank screen.
export default defineConfig({
  plugins: [react(), keepEmbedPlaceholder()],

  // Assets are requested as /admin/assets/…, which is where internal/admin
  // mounts the built tree. Changing this without changing spa.go produces a
  // page that loads its HTML and nothing else.
  base: '/admin/',

  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsDir: 'assets',
    modulePreload: { polyfill: false },
    // No sourcemaps in the artefact: this ships inside the release binary, and
    // a .map beside it is a second copy of the source in every deployment.
    sourcemap: false,
  },

  server: {
    // `npm run dev` runs the real Go backend on :8080 and proxies to it, rather
    // than mocking the API. A dev server that answers from fixtures is one that
    // agrees with the frontend about a shape the backend does not have.
    proxy: {
      '/admin/api': 'http://127.0.0.1:8080',
      '/admin/lang': 'http://127.0.0.1:8080',
    },
  },
})
