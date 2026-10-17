import assert from 'node:assert/strict';
import { createHash, randomBytes, randomUUID } from 'node:crypto';
import http from 'node:http';
import net from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';
import { build } from 'esbuild';
import { Socket } from './parity-devices.mjs';
import { request, fixtureUsers, fixturePassword } from './parity-client.mjs';
import { fixtureDatabase, seedParity } from './seed-parity.mjs';

// Fixed local fixtures: real HTTP/TCP, Go WebSockets, encryption and the production stream multiplexer.
const base = 'http://127.0.0.1:28081';
const MiB = 1024 * 1024;
const sockets = [];
const deviceId = randomUUID();
const compiled = await build({ stdin: { contents: [
  "export { BulkCipher } from './shared/remote-chat/bulkCipher.ts';",
  "export { BulkTransport } from './shared/remote-chat/bulkTransport.ts';",
  "export { MappingStreamMux, STREAM_MANIFEST } from './shared/port-mapping/streamMux.ts';",
].join('\n'), resolveDir: process.cwd() }, bundle: true, platform: 'node', format: 'esm', write: false });
const { BulkCipher, BulkTransport, MappingStreamMux, STREAM_MANIFEST } = await import(
  `data:text/javascript;base64,${Buffer.from(compiled.outputFiles[0].text).toString('base64')}`);

async function socket(path, authentication) {
  const connection = new Socket(base.replace('http:', 'ws:') + path);
  sockets.push(connection);
  connection.ws.removeAllListeners('message');
  connection.binary = () => assert.fail('unexpected binary record');
  connection.ws.on('message', (raw, binary) => {
    if (binary) connection.binary(raw.subarray(5 + raw[4]));
    else { connection.frames.push({ body: JSON.parse(raw.toString()), bytes: raw.length }); connection.wake(); }
  });
  await connection.send({ type: 'authenticate', ...authentication });
  return connection;
}

async function pair(token) {
  const registration = await socket('/device-switch', { accessToken: token, deviceId,
    name: 'Local port stream fixture', platform: 'windows', appVersion: '2.0.0', capabilities: [] });
  await registration.next('authenticated');
  const authentication = { accessToken: token, deviceId, transportVersion: 2, binaryRelay: true, fileBulkV1: true };
  const desktop = await socket('/device-chat', { ...authentication, role: 'desktop' });
  await desktop.next('registered');
  const mobile = await socket('/device-chat', { ...authentication, role: 'mobile', publicKey: 'ab'.repeat(32) });
  const { body: paired } = await mobile.next('paired');
  await desktop.next('peer-open');
  return { desktop, mobile, paired };
}

function link(pair, bytesPerSecond) {
  const identity = { transferId: randomUUID(), epoch: randomUUID() }, root = randomBytes(32);
  const id = Buffer.from(pair.paired.sessionId);
  const prefix = Buffer.concat([Buffer.from('CSF1'), Buffer.from([id.length]), id]);
  const abort = new AbortController();
  let wireBytes = 0, receiving = Promise.resolve(), failure;
  const transports = [pair.desktop, pair.mobile].map((connection, side) => {
    const send = record => new Promise((resolve, reject) => {
      if (side === 0) wireBytes += record.length + prefix.length;
      connection.ws.send(Buffer.concat([prefix, record]), { binary: true }, error => error ? reject(error) : resolve());
    });
    const transport = new BulkTransport({ available: () => !connection.closed, send,
      sendBatch: records => Promise.all(records.map(send)).then(() => {}) });
    transport.setMode('relay');
    connection.binary = record => {
      if (!bytesPerSecond || side === 0) { transport.receive(record, 'relay'); return; }
      // Ordered, lossless receive pacing makes queueing reproducible without a cloud/network bottleneck.
      receiving = receiving.then(async () => {
        if (abort.signal.aborted) return;
        await delay(record.length / bytesPerSecond * 1000, undefined, { signal: abort.signal });
        transport.receive(record, 'relay');
      }).catch(error => { if (!abort.signal.aborted) failure = error; });
    };
    return transport;
  });
  const muxes = transports.map((transport, side) => new MappingStreamMux({ transport, identity,
    cipher: new BulkCipher(root, { ...identity, manifestId: STREAM_MANIFEST, desktop: side === 0,
      sessionId: pair.paired.sessionId, desktopKey: 'host', clientKey: 'viewer' }) }));
  return { muxes, bytes: () => wireBytes, check: () => { if (failure) throw failure; },
    close: () => { abort.abort(); muxes.forEach(mux => mux.close()); transports.forEach(value => value.close()); } };
}

function bridge(tcp, stream) {
  tcp.setNoDelay(true);
  tcp.on('error', () => stream.close());
  const upload = (async () => {
    for await (const bytes of tcp.iterator({ destroyOnReturn: false })) await stream.write(bytes);
    await stream.end();
  })();
  const download = (async () => {
    for (;;) {
      const { data, eof } = await stream.read();
      if (data.length) await new Promise((resolve, reject) =>
        tcp.write(data, error => error ? reject(error) : resolve()));
      if (eof) { await new Promise(resolve => tcp.end(resolve)); return; }
    }
  })();
  // Test cleanup can close sockets with a read or EOF acknowledgement still pending.
  void Promise.all([upload, download]).catch(() => { tcp.destroy(); stream.close(); });
}

async function listen(server) {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return server.address().port;
}

async function download(port, expectedHash) {
  const started = performance.now();
  let firstByte, size = 0;
  const digest = createHash('sha256');
  await new Promise((resolve, reject) => {
    const request = http.get({ hostname: '127.0.0.1', port, path: '/', agent: false }, response => {
      response.on('error', reject).on('data', bytes => {
        firstByte ??= performance.now() - started; size += bytes.length; digest.update(bytes);
      }).on('end', resolve);
    }).on('error', reject);
    request.setTimeout(45_000, () => request.destroy(new Error('Local port stream stalled')));
  });
  assert.equal(digest.digest('hex'), expectedHash, 'HTTP response SHA-256 mismatch');
  return { seconds: (performance.now() - started) / 1000, firstByteMs: firstByte, size };
}

async function mappedDownload(pair, options) {
  const connection = link(pair, options.rate);
  const peers = new Set();
  const listener = net.createServer({ allowHalfOpen: true }, tcp => {
    const id = randomUUID(), target = net.connect({ host: '127.0.0.1', port: options.origin, allowHalfOpen: true });
    peers.add(tcp); peers.add(target);
    tcp.on('close', () => peers.delete(tcp)); target.on('close', () => peers.delete(target));
    const host = connection.muxes[0].open(id, () => target.destroy());
    const client = connection.muxes[1].open(id, () => tcp.destroy());
    bridge(target, host); bridge(tcp, client);
  });
  const port = await listen(listener);
  try {
    const results = await Promise.all(Array.from({ length: options.concurrent ?? 1 },
      () => download(port, options.hash)));
    connection.check();
    const size = results.reduce((sum, result) => sum + result.size, 0);
    const seconds = Math.max(...results.map(result => result.seconds));
    const overhead = connection.bytes() / size;
    console.log(JSON.stringify({ label: options.label, mib: size / MiB, seconds: +seconds.toFixed(3),
      mibPerSecond: +(size / MiB / seconds).toFixed(2), wireMiB: +(connection.bytes() / MiB).toFixed(3),
      wireRatio: +overhead.toFixed(3),
      firstByteMs: +Math.max(...results.map(result => result.firstByteMs)).toFixed(1) }));
    assert.ok(overhead < 1.05, 'reliable relay amplified stream bytes');
  } finally {
    connection.close(); peers.forEach(peer => peer.destroy());
    await new Promise(resolve => listener.close(resolve));
  }
}

await seedParity();
const login = await request(base, 'POST', '/auth/login', {
  body: { email: fixtureUsers.admin.email, password: fixturePassword },
});
assert.equal(login.status, 201);
const token = login.body.accessToken;
const endpoint = '/admin/api/chat-settings';
const initial = await request(base, 'GET', endpoint, { token });
assert.equal(initial.status, 200);
const body = randomBytes(4 * MiB), hash = createHash('sha256').update(body).digest('hex');
const origin = http.createServer((_request, response) => {
  response.writeHead(200, { 'Content-Length': body.length, Connection: 'close' }); response.end(body);
});
try {
  assert.equal((await request(base, 'PATCH', endpoint, { token, body: { ...initial.body,
    fileBulkEnabled: 1, relayMaxMbPerSecond: -1, relayMaxFramesPerSecond: -1 } })).status, 200);
  const port = await listen(origin), connection = await pair(token);
  console.log(JSON.stringify({ label: 'direct HTTP', ...await download(port, hash) }));
  await mappedDownload(connection, { origin: port, hash, label: 'mapped HTTP, localhost relay' });
  await mappedDownload(connection, { origin: port, hash, label: 'four concurrent HTTP requests', concurrent: 4 });
  await mappedDownload(connection, { origin: port, hash, label: 'mapped HTTP, ordered receive at 1 MiB/s', rate: MiB });
} finally {
  for (const socket of sockets) socket.ws.terminate();
  origin.closeAllConnections(); await new Promise(resolve => origin.close(resolve));
  assert.equal((await request(base, 'PATCH', endpoint, { token, body: initial.body })).status, 200);
  const db = await fixtureDatabase('admin_go');
  try { await db.query('DELETE FROM remote_devices WHERE "ownerId"=$1 AND "deviceId"=$2',
    [fixtureUsers.admin.id, deviceId]); } finally { await db.end(); }
}
