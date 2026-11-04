import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'platform.json'), 'utf8'));
for (const { source, target } of manifest.mirrors) {
  const content = `// Generated from packages/i18n/${source}. Do not edit.\n`
    + fs.readFileSync(path.join(root, source), 'utf8').replaceAll('\r\n', '\n');
  const destination = path.resolve(root, '../..', target);
  if (process.argv.includes('--check')) {
    if (fs.readFileSync(destination, 'utf8').replaceAll('\r\n', '\n') !== content) {
      throw new Error(`Regenerate platform copy: ${target}`);
    }
  } else fs.writeFileSync(destination, content);
}
console.log('Backend platform translations are current.');
