import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

/** Verify the additive SSH upgrade against the isolated parity database. */
export async function checkSshProfilesMigration(database) {
  const migration = await readFile(new URL('../sql/20261024-ssh-profiles.sql', import.meta.url), 'utf8');
  const embedded = await readFile(new URL('../internal/migrations/017_ssh_profiles.sql', import.meta.url), 'utf8');
  assert.equal(migration, embedded, 'new and upgraded installations must use the same SSH profile schema');
  await database.query('BEGIN');
  try {
    const owner = (await database.query('SELECT id FROM users ORDER BY id LIMIT 1')).rows[0].id;
    const config = { name: 'Migration fixture', host: 'example.test', port: 22, username: 'fixture' };
    await database.query(`INSERT INTO synced_ssh_profiles("ownerId","profileId",revision,config)
      VALUES($1,'migration-saved',7,$2),($1,'migration-deleted',8,NULL)`, [owner, JSON.stringify(config)]);
    await database.query(migration);
    await database.query(migration);
    const saved = await database.query(`SELECT "profileId",revision::int,config FROM synced_ssh_profiles
      WHERE "ownerId"=$1 AND "profileId" IN ('migration-saved','migration-deleted') ORDER BY "profileId"`, [owner]);
    assert.deepEqual(saved.rows, [
      { profileId: 'migration-deleted', revision: 8, config: null },
      { profileId: 'migration-saved', revision: 7, config },
    ], 'migration reruns must preserve profiles, versions and deletion markers');
  } finally { await database.query('ROLLBACK'); }
  console.log('PASS SSH profile upgrade matches bootstrap and preserves profiles and deletion markers on rerun');
}
