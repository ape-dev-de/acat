// Bundles the renderer (ACS SDK included) into dist/ for the sandboxed window.
import { build } from 'esbuild';
import { copyFile, mkdir } from 'node:fs/promises';

await mkdir('dist', { recursive: true });
await build({
  entryPoints: ['src/renderer/call.js'],
  bundle: true,
  format: 'iife',
  platform: 'browser',
  target: 'chrome130',
  minify: true,
  legalComments: 'external', // keeps the SDK's license comments in dist/call.bundle.js.LEGAL.txt
  outfile: 'dist/call.bundle.js',
  logLevel: 'warning',
});
await copyFile('src/renderer/call.html', 'dist/call.html');
await copyFile('src/renderer/call.css', 'dist/call.css');
