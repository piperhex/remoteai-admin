import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

const provenance = JSON.parse(readFileSync(new URL('../testdata/legacy-source.json', import.meta.url), 'utf8'));
for (const [file, expected] of Object.entries(provenance.fixtures)) {
  const content = readFileSync(new URL(`../${file}`, import.meta.url), 'utf8').replaceAll('\r\n', '\n');
  assert.equal(createHash('sha256').update(content).digest('hex'), expected, `Historical fixture changed: ${file}`);
}
console.log('Historical contract and validation fixtures match their recorded source.');
