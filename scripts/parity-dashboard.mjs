import assert from 'node:assert/strict';
import { fixtureDatabase } from './seed-parity.mjs';

const hourMs = 60 * 60 * 1000;
const dayMs = 24 * hourMs;
const platforms = ['windows', 'macos', 'linux', 'android', 'ios'];
const day = (value, offset) => new Date(new Date(value).getTime() + offset).toISOString().slice(0, 10);
const counts = (rows) => platforms.map((name) => ({
  name, value: rows.filter((row) => row.platform === name).length,
}));
const uniqueDevices = (rows) => [...new Map(rows.map((row) => [row.deviceId, row])).values()];
const activityCounts = (rows) => platforms.map((name) => ({
  name, value: uniqueDevices(rows.filter((row) => row.platform === name)).length,
}));

// Read raw fixture rows so the expected day buckets do not repeat either server's SQL.
async function readRows(database) {
  const client = await fixtureDatabase(database);
  try {
    const users = await client.query('SELECT "createdAt" FROM users');
    const installations = await client.query('SELECT "firstSeenAt", platform FROM device_installations');
    const activity = await client.query(`SELECT "createdAt", "deviceId", platform
      FROM device_telemetry_events WHERE "eventType"='activity'`);
    return { users: users.rows, installations: installations.rows, activity: activity.rows };
  } finally { await client.end(); }
}

export function expectedDashboardDays({ rows, days, now, offset }) {
  const endDate = day(now, offset);
  const dates = Array.from({ length: days }, (_, index) =>
    day(new Date(endDate).getTime() + (index + 1 - days) * dayMs, 0));
  const inRange = (value) => day(value, offset) >= dates[0] && day(value, offset) <= endDate;
  const newUsers = rows.users.filter((row) => inRange(row.createdAt));
  const newInstallations = rows.installations.filter((row) => inRange(row.firstSeenAt));
  let total = rows.installations.length - newInstallations.length;
  const trend = dates.map((date) => {
    const installed = newInstallations.filter((row) => day(row.firstSeenAt, offset) === date);
    total += installed.length;
    return { date, users: newUsers.filter((row) => day(row.createdAt, offset) === date).length,
      installations: installed.length, totalInstallations: total, platforms: counts(installed) };
  });
  const activeOn = (date) => rows.activity.filter((row) => day(row.createdAt, offset) === date);
  const dailyActiveTrend = dates.map((date) => {
    const values = activityCounts(activeOn(date));
    return { date, total: values.reduce((sum, item) => sum + item.value, 0), platforms: values };
  });
  return { range: { days, startDate: dates[0], endDate }, trend, dailyActiveTrend,
    dailyActivePlatforms: activityCounts(activeOn(endDate)),
    summary: { newUsers: newUsers.length, newInstallations: newInstallations.length,
      dailyActiveUsers: uniqueDevices(activeOn(endDate)).length } };
}

function assertDayResults(body, expected) {
  const remaining = structuredClone(body);
  for (const field of ['range', 'trend', 'dailyActiveTrend', 'dailyActivePlatforms']) {
    assert.deepEqual(body[field], expected[field], `dashboard ${field} must match its documented time zone`);
    delete remaining[field];
  }
  for (const [field, value] of Object.entries(expected.summary)) {
    assert.equal(body.summary[field], value, `dashboard ${field} must match fixture rows`);
    delete remaining.summary[field];
  }
  return remaining;
}

export async function checkDashboard(pair, days) {
  const now = Date.now();
  const expected = {};
  for (const [side, database, offset] of [['legacy', 'legacy', 0], ['modern', 'admin_go', 8 * hourMs]]) {
    expected[side] = expectedDashboardDays({ rows: await readRows(database), days, now, offset });
  }
  const response = await pair.request('GET', `/admin/api/dashboard/overview?days=${days}`);
  for (const side of ['legacy', 'modern']) assert.equal(response[side].status, 200);
  // The intentional UTC-to-Beijing change is checked above; all other fields still match exactly.
  pair.check(`dashboard ${days} days`, response, (body, side) => assertDayResults(body, expected[side]));
}
