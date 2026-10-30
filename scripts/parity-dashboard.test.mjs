import assert from 'node:assert/strict';
import { test } from 'node:test';
import { expectedDashboardDays } from './parity-dashboard.mjs';

test('fixture expectations distinguish Beijing midnight and deduplicate daily activity', () => {
  const now = Date.parse('2026-10-30T01:00:00Z');
  const before = '2026-10-29T15:59:59Z';
  const after = '2026-10-29T16:00:00Z';
  const rows = { users: [{ createdAt: before }, { createdAt: after }],
    installations: [before, after].map((firstSeenAt) => ({ firstSeenAt, platform: 'windows' })),
    activity: [before, after, after].map((createdAt) => ({ createdAt, deviceId: 'one', platform: 'windows' })) };
  const legacy = expectedDashboardDays({ rows, days: 1, now, offset: 0 });
  const modern = expectedDashboardDays({ rows, days: 1, now, offset: 8 * 60 * 60 * 1000 });
  assert.deepEqual(legacy.summary, { newUsers: 0, newInstallations: 0, dailyActiveUsers: 0 });
  assert.deepEqual(modern.summary, { newUsers: 1, newInstallations: 1, dailyActiveUsers: 1 });
  assert.equal(modern.trend[0].totalInstallations, 2);
  assert.equal(modern.dailyActiveTrend[0].total, 1);
});
