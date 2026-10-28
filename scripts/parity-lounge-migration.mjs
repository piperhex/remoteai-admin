import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';

/** Verify moderation-only storage and repeatable upgrades in the isolated fixture database. */
export async function checkLoungeMigration(database) {
  const migration = await readFile(new URL('../sql/20261028-lounge-moderation.sql', import.meta.url), 'utf8');
  const embedded = await readFile(new URL('../internal/migrations/019_lounge_moderation.sql', import.meta.url), 'utf8');
  const schema = migration.trim().replace(/^BEGIN;\s*/, '').replace(/\s*COMMIT;$/, '');
  assert.equal(schema, embedded.trim(), 'new and upgraded installations must use the same lounge schema');
  const user = randomUUID();
  const expires = new Date('2030-01-01T00:00:00Z');
  try {
    await database.query(`INSERT INTO users(id,email,"passwordHash",role,disabled)
      VALUES($1,$2,'unused-migration-fixture','user',false)`, [user, `${user}@example.test`]);
    await database.query('INSERT INTO lounge_user_policies(user_id) VALUES($1)', [user]);
    const defaults = await database.query('SELECT moderator,muted_until FROM lounge_user_policies WHERE user_id=$1',
      [user]);
    assert.deepEqual(defaults.rows, [{ moderator: false, muted_until: null }]);
    await database.query('UPDATE lounge_user_policies SET moderator=true,muted_until=$2 WHERE user_id=$1',
      [user, expires]);
    await database.query(migration);
    await database.query(migration);
    const saved = await database.query('SELECT moderator,muted_until FROM lounge_user_policies WHERE user_id=$1',
      [user]);
    assert.deepEqual(saved.rows, [{ moderator: true, muted_until: expires }],
      'migration reruns must preserve moderator permissions and mute expiry');
    const columns = await database.query(`SELECT column_name FROM information_schema.columns
      WHERE table_schema='public' AND table_name='lounge_user_policies' ORDER BY ordinal_position`);
    assert.deepEqual(columns.rows.map(({ column_name }) => column_name), ['user_id', 'moderator', 'muted_until'],
      'lounge storage must contain only current moderation policy');
    await database.query('DELETE FROM users WHERE id=$1', [user]);
    const deleted = await database.query('SELECT user_id FROM lounge_user_policies WHERE user_id=$1', [user]);
    assert.deepEqual(deleted.rows, [], 'deleting a user must remove their moderation policy');
  } finally { await database.query('DELETE FROM users WHERE id=$1', [user]); }
  console.log('PASS lounge upgrade matches bootstrap, preserves policies on rerun and cascades user deletion');
}
