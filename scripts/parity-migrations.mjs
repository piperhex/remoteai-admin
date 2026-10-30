import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { checkSshProfilesMigration } from './parity-ssh-profiles-migration.mjs';
import { checkLoungeMigration } from './parity-lounge-migration.mjs';

const require = createRequire(new URL('../testdata/tools/package.json', import.meta.url));
const { Client } = require('pg');
const bootstrapDatabase = 'admin_go_bootstrap';
const containerName = 'codex-admin-bootstrap-test';
const projectName = 'codex-admin-parity';
// Upgraded installations retain these rollback tables; new installations do not create them.
const retiredUsageTables = ['official_usage_devices', 'official_usage_accounts', 'official_usage_minutes',
  'official_quota_observations', 'official_quota_declines'];
const goOnlyTables = ['token_cost_preset_settings', 'user_login_locks', 'chat_relay_user_limits',
  'chat_relay_user_months', 'chat_relay_user_hours', 'chat_relay_budgets', 'chat_relay_bulk_leases',
  'chat_push_subscriptions', 'chat_push_deliveries', 'desktop_service_credentials',
  'official_usage_v2_devices', 'official_usage_v2_accounts', 'official_usage_v2_minutes',
  'official_quota_v2_observations', 'official_quota_v2_declines',
  'synced_aggregates', 'update_sharing_settings', 'device_port_mappings', 'update_download_reports',
  'synced_ssh_profiles', 'lounge_user_policies', 'lounge_user_blocks',
  'lounge_direct_permissions', 'lounge_profiles', 'lounge_nicknames',
  'lounge_contact_remarks', 'lounge_notification_settings', 'provider_market_items', 'provider_market_usage'];

function docker(...args) {
  return execFileSync('docker', args, { encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 }).trim();
}

function serviceContainer(service) {
  const id = docker('ps', '-q', '--filter', `label=com.docker.compose.project=${projectName}`,
    '--filter', `label=com.docker.compose.service=${service}`);
  assert.ok(id && !id.includes('\n'), `exactly one local ${service} container is required`);
  return id;
}

function schema(postgres, database, legacyOnly = false, excludedTables = []) {
  const excluded = legacyOnly ? [...goOnlyTables, ...retiredUsageTables] : excludedTables;
  const dump = docker('exec', postgres, 'pg_dump', '-U', 'parity', '-d', database,
    '--schema-only', '--no-owner', '--no-privileges',
    ...excluded.map((table) => `--exclude-table=public.${table}`))
    .split('\n').filter((line) => !line.startsWith('\\restrict ') && !line.startsWith('\\unrestrict '))
    .join('\n');
  // Additive columns are asserted separately; the frozen Nest schema cannot contain them.
  return legacyOnly ? dump.replace(/^    "gui(?:Account|Provider)Id" character varying\(120\),?\n/gm, '')
    .replace(/^    "(?:requestOptions|balanceScript|marketSource)" jsonb,?\n/gm, '')
    .replace(/,\n\);/g, '\n);') : dump;
}

async function checkGuiColumns(name) {
  const client = await database(name);
  try {
    const columns = await client.query(`SELECT column_name, data_type, character_maximum_length, is_nullable
      FROM information_schema.columns WHERE table_schema='public' AND table_name='remote_devices'
      AND column_name IN ('guiAccountId', 'guiProviderId') ORDER BY column_name`);
    assert.deepEqual(columns.rows, ['guiAccountId', 'guiProviderId'].map((column_name) => ({
      column_name, data_type: 'character varying', character_maximum_length: 120, is_nullable: 'YES',
    })));
  } finally { await client.end(); }
}

async function checkProviderOptionsColumn(name) {
  const client = await database(name);
  try {
    const columns = await client.query(`SELECT column_name, data_type, is_nullable
      FROM information_schema.columns WHERE table_schema='public' AND table_name='synced_providers'
      AND column_name IN ('requestOptions', 'balanceScript', 'marketSource') ORDER BY column_name`);
    assert.deepEqual(columns.rows, ['balanceScript', 'marketSource', 'requestOptions'].map((column_name) => ({
      column_name, data_type: 'jsonb', is_nullable: 'YES',
    })));
  } finally { await client.end(); }
}

async function database(name) {
  assert.ok(['legacy', 'admin_go', bootstrapDatabase].includes(name));
  const client = new Client({ host: '127.0.0.1', port: 15432, user: 'parity',
    password: 'local-parity-only', database: name });
  await client.connect();
  return client;
}

async function startBootstrap(config) {
  const env = config.Config.Env.filter((item) => !/^(POSTGRES_DB|POSTGRES_DB_SYNCHRONIZE|CHAT_STUN_PORT)=/.test(item));
  const network = Object.keys(config.NetworkSettings.Networks)[0];
  assert.equal(network, `${projectName}_default`, 'migration tests only use the isolated fixture network');
  docker('run', '-d', '--name', containerName, '--network', network, '-p', '127.0.0.1:28082:8080',
    ...env.flatMap((item) => ['-e', item]), '-e', `POSTGRES_DB=${bootstrapDatabase}`,
    '-e', 'POSTGRES_DB_SYNCHRONIZE=true', '-e', 'CHAT_STUN_PORT=0',
    process.env.PARITY_GO_IMAGE || config.Config.Image);
  for (let attempt = 0; attempt < 60; attempt += 1) {
    try {
      const response = await fetch('http://127.0.0.1:28082/auth/me');
      if (response.status === 401) return;
    } catch { /* The container is still connecting and initializing its empty schema. */ }
    await delay(250);
  }
  throw new Error(`Bootstrap service did not start:\n${docker('logs', containerName)}`);
}

async function stopBootstrap() {
  const id = docker('ps', '-aq', '--filter', `name=^/${containerName}$`);
  if (id) docker('rm', '-f', containerName);
}

async function checkUsageMigration(postgres) {
  const client = await database('admin_go');
  const baseline = schema(postgres, 'admin_go');
  const accountId = 'quota-v2-migration-sentinel';
  const owner = (await client.query('SELECT id FROM users ORDER BY id LIMIT 1')).rows[0].id;
  try {
    await client.query('INSERT INTO official_usage_accounts(owner_id,account_id,label) VALUES($1,$2,$3)',
      [owner, accountId, 'preserve old usage']);
    const migration = readFileSync(new URL('../sql/20261009-official-usage-v2.sql', import.meta.url), 'utf8');
    await client.query(migration);
    await client.query(migration);
    assert.equal(schema(postgres, 'admin_go'), baseline, 'usage migration reruns must preserve schema');
    const old = await client.query('SELECT label FROM official_usage_accounts WHERE owner_id=$1 AND account_id=$2',
      [owner, accountId]);
    assert.deepEqual(old.rows, [{ label: 'preserve old usage' }]);
    const fresh = await client.query('SELECT account_id FROM official_usage_v2_accounts WHERE account_id=$1', [accountId]);
    assert.deepEqual(fresh.rows, [], 'legacy usage must not be imported into v2 statistics');
    console.log('PASS usage v2 migration preserves old records without importing them and supports reruns');
  } finally {
    await client.query('DELETE FROM official_usage_accounts WHERE owner_id=$1 AND account_id=$2', [owner, accountId]);
    await client.end();
  }
}

async function checkPortMappingMigration(postgres) {
  const client = await database('admin_go');
  const baseline = schema(postgres, 'admin_go');
  const migration = readFileSync(new URL('../sql/20261015-port-mappings.sql', import.meta.url), 'utf8');
  const embedded = readFileSync(new URL('../internal/migrations/015_port_mappings.sql', import.meta.url), 'utf8');
  assert.equal(migration, embedded, 'new and upgraded installations must use the same port mapping schema');
  try {
    await client.query('BEGIN');
    const owner = (await client.query('SELECT id FROM users ORDER BY id LIMIT 1')).rows[0].id;
    const device = 'cc22cc22-0000-4000-8000-000000000001';
    await client.query(`INSERT INTO remote_devices("ownerId","deviceId",name,platform)
      VALUES($1,$2,'Port mapping migration fixture','Windows')`, [owner, device]);
    const mappings = [{ id: 'migration-sentinel', remoteHost: '127.0.0.1', remotePort: 3000, localPort: 13000 }];
    await client.query(`INSERT INTO device_port_mappings("ownerId","deviceId",revision,mappings)
      VALUES($1,$2,7,$3)`, [owner, device, JSON.stringify(mappings)]);
    await client.query(migration);
    await client.query(migration);
    const saved = await client.query(`SELECT revision::int,mappings FROM device_port_mappings
      WHERE "ownerId"=$1 AND "deviceId"=$2`, [owner, device]);
    assert.deepEqual(saved.rows, [{ revision: 7, mappings }], 'migration reruns must preserve saved mappings');
  } finally {
    await client.query('ROLLBACK');
    await client.end();
  }
  assert.equal(schema(postgres, 'admin_go'), baseline, 'port mapping migration reruns must preserve schema');
  console.log('PASS port mapping upgrade matches bootstrap and preserves existing mappings on rerun');
}

async function checkDownloadReportMigration(postgres) {
  const client = await database('admin_go');
  const baseline = schema(postgres, 'admin_go');
  const migration = readFileSync(new URL('../sql/20261017-update-download-reports.sql', import.meta.url), 'utf8');
  const embeddedPath = new URL('../internal/migrations/016_update_download_reports.sql', import.meta.url);
  const embedded = readFileSync(embeddedPath, 'utf8');
  assert.equal(migration, embedded, 'new and upgraded installations must use the same download report schema');
  try {
    await client.query('BEGIN');
    const owner = (await client.query('SELECT id FROM users ORDER BY id LIMIT 1')).rows[0].id;
    const reportId = 'cc22cc22-0000-4000-8000-000000000002';
    await client.query(`INSERT INTO update_download_reports
      (id,user_id,artifact,platform,outcome,peer_bytes,server_bytes,origin_bytes,failures)
      VALUES($1,$2,'migration-sentinel','windows','success',512,1024,256,'[]')`, [reportId, owner]);
    await client.query(migration);
    await client.query(migration);
    const saved = await client.query(`SELECT outcome,peer_bytes::int,server_bytes::int,origin_bytes::int,failures
      FROM update_download_reports WHERE id=$1 AND user_id=$2`, [reportId, owner]);
    assert.deepEqual(saved.rows, [{ outcome: 'success', peer_bytes: 512, server_bytes: 1024,
      origin_bytes: 256, failures: [] }], 'migration reruns must preserve download reports');
  } finally {
    await client.query('ROLLBACK');
    await client.end();
  }
  assert.equal(schema(postgres, 'admin_go'), baseline, 'download report migration reruns must preserve schema');
  console.log('PASS download report upgrade matches bootstrap and preserves reports on rerun');
}

async function checkMarketSourceMigration(postgres) {
  const client = await database('admin_go');
  const baseline = schema(postgres, 'admin_go');
  const migration = readFileSync(new URL('../sql/20261030_provider_market_page.sql', import.meta.url), 'utf8');
  const source = { id: '11111111-1111-4111-8111-111111111111', page: { url: 'https://example.test/dashboard' } };
  const id = 'cc22cc22-0000-4000-8000-000000000003';
  try {
    const owner = (await client.query('SELECT id FROM users ORDER BY id LIMIT 1')).rows[0].id;
    await client.query(`INSERT INTO synced_providers
      (id,"ownerId","providerId",name,"baseUrl","apiKey",model,"apiFormat","marketSource")
      VALUES($1,$2,'market-migration-sentinel','Keep provider','https://example.test/v1','fixture','test','responses',$3)`,
    [id, owner, source]);
    await client.query(migration);
    await client.query(migration);
    const saved = await client.query('SELECT name,"marketSource" FROM synced_providers WHERE id=$1', [id]);
    assert.deepEqual(saved.rows, [{ name: 'Keep provider', marketSource: source }]);
    assert.equal(schema(postgres, 'admin_go'), baseline, 'market page migration reruns must preserve schema');
  } finally {
    await client.query('DELETE FROM synced_providers WHERE id=$1', [id]);
    await client.end();
  }
  console.log('PASS market page upgrade preserves provider data and page access on rerun');
}

export async function runMigrations() {
  const postgres = serviceContainer('postgres');
  const config = JSON.parse(docker('inspect', serviceContainer('admin-go')))[0];
  const original = schema(postgres, 'legacy');
  await checkGuiColumns('admin_go');
  await checkProviderOptionsColumn('admin_go');
  assert.equal(schema(postgres, 'admin_go', true), original, 'existing Go database must retain every legacy schema object');
  console.log('PASS existing PostgreSQL columns, defaults, indexes and constraints match');
  await checkUsageMigration(postgres);
  await checkPortMappingMigration(postgres);
  await checkDownloadReportMigration(postgres);
  await checkMarketSourceMigration(postgres);
  const migrationDatabase = await database('admin_go');
  try {
    await checkSshProfilesMigration(migrationDatabase);
    await checkLoungeMigration(migrationDatabase);
  } finally { await migrationDatabase.end(); }
  await stopBootstrap();
  const admin = await database('legacy');
  try {
    // This fixed, isolated database is owned exclusively by this test.
    await admin.query(`DROP DATABASE IF EXISTS ${bootstrapDatabase} WITH (FORCE)`);
    await admin.query(`CREATE DATABASE ${bootstrapDatabase}`);
    await startBootstrap(config);
    await checkGuiColumns(bootstrapDatabase);
    await checkProviderOptionsColumn(bootstrapDatabase);
    assert.equal(schema(postgres, bootstrapDatabase, true), original, 'empty-database initialization must reproduce legacy schema');
    console.log('PASS empty database initialized with the complete legacy schema');
    const fresh = await database(bootstrapDatabase);
    try {
      const tables = await fresh.query(`SELECT count(*)::int AS total FROM information_schema.tables
        WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`);
      assert.equal(tables.rows[0].total, 30 + goOnlyTables.length);
      assert.equal(schema(postgres, bootstrapDatabase), schema(postgres, 'admin_go', false, retiredUsageTables),
        'new and upgraded databases must have identical active Go tables');
      const guiSchema = schema(postgres, bootstrapDatabase);
      const guiMigration = readFileSync(new URL('../sql/20260924-device-gui-model-selection.sql', import.meta.url), 'utf8');
      await fresh.query(guiMigration);
      await fresh.query(guiMigration);
      assert.equal(schema(postgres, bootstrapDatabase), guiSchema, 'GUI migration reruns must preserve schema');
      const pricing = { models: [], sentinel: 'preserve saved pricing' };
      await fresh.query("INSERT INTO token_cost_preset_settings (id, presets) VALUES ('current', $1)", [pricing]);
      const migration = readFileSync(new URL('../sql/20260923-token-cost-presets.sql', import.meta.url), 'utf8');
      await fresh.query(migration);
      await fresh.query(migration);
      assert.deepEqual((await fresh.query('SELECT presets FROM token_cost_preset_settings')).rows,
        [{ presets: pricing }]);
      console.log('PASS pricing migration is additive and preserves saved presets on rerun');
      await fresh.query('CREATE TABLE migration_sentinel (id integer PRIMARY KEY, message text NOT NULL)');
      await fresh.query('INSERT INTO migration_sentinel VALUES (1,$1)', ['retain customer data']);
      const withSentinel = schema(postgres, bootstrapDatabase);
      await stopBootstrap();
      await startBootstrap(config);
      assert.equal(schema(postgres, bootstrapDatabase), withSentinel, 'startup must not alter an existing database');
      assert.deepEqual((await fresh.query('SELECT * FROM migration_sentinel')).rows,
        [{ id: 1, message: 'retain customer data' }]);
      console.log('PASS repeated startup preserves existing schema and customer data');
    } finally { await fresh.end(); }
  } finally {
    await stopBootstrap();
    await admin.query(`DROP DATABASE IF EXISTS ${bootstrapDatabase} WITH (FORCE)`);
    await admin.end();
  }
  return [{ label: 'existing schema unchanged' }, { label: 'empty schema initialization' },
    { label: 'usage v2 upgrade retains legacy data without importing it' },
    { label: 'port mapping upgrade preserves saved mappings on rerun' },
    { label: 'download report upgrade preserves saved reports on rerun' },
    { label: 'market page upgrade preserves providers and page access on rerun' },
    { label: 'SSH profile upgrade preserves saved profiles and deletion markers on rerun' },
    { label: 'lounge upgrade preserves moderation policies and deletes them with their user' },
    { label: 'idempotent initialization and data preservation' }];
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await runMigrations();
