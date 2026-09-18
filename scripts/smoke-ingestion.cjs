// Real portable ingestion + SQLite retrieval using an isolated, deterministic model stub.
// This verifies integration, not model quality or company network compatibility.
const { Backend } = require('../desktop/src/backend.cjs');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const http = require('node:http');
const path = require('node:path');
const os = require('node:os');
const { spawnSync } = require('node:child_process');
const JSZip = require('../frontend/node_modules/jszip');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

(async () => {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'assistant-ingestion-'));
  const resources = path.resolve(process.argv[2] || `dist/portable/${process.platform}-${process.arch}`);
  const suffix = process.platform === 'win32' ? '.exe' : '';
  const makeBackend = data => new Backend({ executable: path.join(resources, `server${suffix}`), resources, data });
  let backend = makeBackend(path.join(temp, 'data'));
  let embeddingCalls = 0, chatCalls = 0, authFailures = 0, token, origin;
  const mock = http.createServer(async (req, res) => {
    try {
      if (req.headers.authorization !== 'Bearer smoke-local-key') {
        authFailures++; res.statusCode = 401; res.end('{"error":"invalid smoke credential"}'); return;
      }
      let raw = ''; for await (const part of req) raw += part;
      const body = raw ? JSON.parse(raw) : {};
      res.setHeader('Content-Type', 'application/json');
      if (req.url.endsWith('/embeddings')) {
        embeddingCalls++;
        const input = Array.isArray(body.input) ? body.input : [body.input];
        res.end(JSON.stringify({ object: 'list', model: body.model, data: input.map((_, index) => ({ object: 'embedding', index, embedding: [1, 0, 0, 0, 0, 0, 0, 0] })), usage: { prompt_tokens: input.length, total_tokens: input.length } }));
      } else if (req.url.endsWith('/chat/completions')) {
        chatCalls++;
        const content = 'Portable smoke integration document. Orion archive contains the sapphire launch code.';
        if (body.stream) {
          res.setHeader('Content-Type', 'text/event-stream');
          res.end(`data: ${JSON.stringify({ id: 'smoke', object: 'chat.completion.chunk', choices: [{ index: 0, delta: { content }, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ id: 'smoke', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`);
        } else res.end(JSON.stringify({ id: 'smoke', object: 'chat.completion', model: body.model, choices: [{ index: 0, message: { role: 'assistant', content }, finish_reason: 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 10, total_tokens: 20 } }));
      } else { res.statusCode = 404; res.end(JSON.stringify({ error: 'Unsupported mock endpoint' })); }
    } catch { res.statusCode = 500; res.end('{"error":"mock request failed"}'); }
  });
  async function request(route, body, method = body === undefined ? 'GET' : 'POST') {
    const form = body instanceof FormData;
    const response = await fetch(origin + '/api/v1' + route, { method, headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(!form && body !== undefined ? { 'Content-Type': 'application/json' } : {}) }, body: body === undefined ? undefined : form ? body : JSON.stringify(body), signal: AbortSignal.timeout(30000) });
    const result = await response.json();
    assert.ok(response.ok && result.success !== false, `${route}: HTTP ${response.status}; ${result.message || result.error?.message || 'request failed'}`);
    return result;
  }
  async function awaitParsed(id) {
    for (let i = 0; i < 120; i++) {
      const { data } = await request(`/knowledge/${id}`);
      assert.notEqual(data.parse_status, 'failed', `Ingestion failed for ${id}: ${data.error_message || 'see retained backend log'}`);
      if (data.parse_status === 'completed' && data.summary_status !== 'pending' && data.summary_status !== 'processing') {
        assert.equal(data.summary_status, 'completed', `LLM summary must complete for ${id}`);
        return data;
      }
      await sleep(1000);
    }
    throw new Error(`Timed out awaiting ingestion ${id}`);
  }
  async function startBackend() {
    const previous = process.env.SSRF_WHITELIST_EXTRA;
    process.env.SSRF_WHITELIST_EXTRA = '127.0.0.1';
    try { return await backend.start(); } finally {
      if (previous === undefined) delete process.env.SSRF_WHITELIST_EXTRA; else process.env.SSRF_WHITELIST_EXTRA = previous;
    }
  }
  try {
    await new Promise(resolve => mock.listen(0, '127.0.0.1', resolve));
    // Only the throwaway child backend inherits this exact loopback allowance.
    origin = await startBackend();
    const credentials = { email: 'ingestion@example.invalid', username: 'Ingestion Smoke', password: 'Ingestion-test-48!Only' };
    await request('/auth/register', credentials);
    const login = await request('/auth/login', credentials);
    token = login.token;
    assert.ok(token, 'login must issue a token');
    const base = `http://127.0.0.1:${mock.address().port}/v1`;
    async function model(type, name) {
      return (await request('/models', { name, type, source: 'remote', parameters: { base_url: base, api_key: 'smoke-local-key', provider: 'openai', interface_type: 'openai', embedding_parameters: { dimension: 8 } } })).data.id;
    }
    const embeddingID = await model('Embedding', 'smoke-embedding');
    const chatID = await model('KnowledgeQA', 'smoke-chat');
    const { data: kb } = await request('/knowledge-bases', { name: 'Portable ingestion smoke', type: 'document', embedding_model_id: embeddingID, summary_model_id: chatID, chunking_config: { chunk_size: 512, chunk_overlap: 40, separators: ['\n\n', '\n'] }, storage_provider_config: { provider: 'local' } });
    const zip = new JSZip();
    zip.file('[Content_Types].xml', '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>');
    zip.file('_rels/.rels', '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>');
    zip.file('word/document.xml', '<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Orion archive office document: the sapphire launch code is AZURE-472. Keep this original citation for the knowledge assistant.</w:t></w:r></w:p></w:body></w:document>');
    const docs = [['smoke.txt', Buffer.from('Orion archive text document: the sapphire launch code is AZURE-472. The archive is maintained by the local portable knowledge assistant.')], ['smoke.docx', await zip.generateAsync({ type: 'nodebuffer' })]];
    const ids = [];
    for (const [name, bytes] of docs) {
      const form = new FormData(); form.append('file', new Blob([bytes]), name);
      const { data } = await request(`/knowledge-bases/${kb.id}/knowledge/file`, form);
      ids.push(data.id); await awaitParsed(data.id);
      console.log(`PASS: ${name} parsed and indexed.`);
    }
    async function verifyRetrieval() {
      for (const [mode, flags] of [['hybrid', {}], ['vector', { disable_keywords_match: true }], ['keyword', { disable_vector_match: true }]]) {
        const { data: results } = await request(`/knowledge-bases/${kb.id}/hybrid-search`, { query_text: 'sapphire', match_count: 10, vector_threshold: 0, keyword_threshold: 0, ...flags });
        for (const id of ids) assert.ok(results.some(r => r.knowledge_id === id && r.content.includes('AZURE-472')), `${mode} retrieval must cite each uploaded document and its actual parsed content`);
      }
    }
    await verifyRetrieval();
    await backend.stop();
    const archive = path.join(temp, 'backup.tar.gz');
    for (const [action, data] of [['backup', path.join(temp, 'data')], ['restore', path.join(temp, 'restored')]]) {
      const result = spawnSync(path.join(resources, `assistant-backup${suffix}`), ['--data-dir', data, '--archive', archive, action], { encoding: 'utf8' });
      assert.equal(result.status, 0, `${action} failed: ${result.stderr}`);
    }
    // Remove the source so successful downloads cannot read the old absolute paths.
    await fs.rm(path.join(temp, 'data'), { recursive: true, force: true });
    backend = makeBackend(path.join(temp, 'restored'));
    origin = await startBackend();
    token = (await request('/auth/login', credentials)).token;
    const previousEmbeddingCalls = embeddingCalls;
    await verifyRetrieval();
    assert.ok(embeddingCalls > previousEmbeddingCalls, 'restored encrypted credential must authorize a fresh embedding request');
    for (let i = 0; i < ids.length; i++) {
      const response = await fetch(`${origin}/api/v1/knowledge/${ids[i]}/download`, { headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(30000) });
      assert.ok(response.ok, `restored original download failed: HTTP ${response.status}`);
      assert.deepEqual(Buffer.from(await response.arrayBuffer()), docs[i][1], 'restored original must match exact uploaded bytes');
    }
    assert.equal(authFailures, 0, 'all model requests must authenticate with the configured mock key');
    assert.ok(embeddingCalls > 0, 'actual embedding HTTP calls required');
    assert.ok(chatCalls > 0, 'actual summary LLM HTTP calls required');
    console.log('PASS: backup/restore to a new directory retained encrypted model credentials, SQLite indexes and byte-identical TXT/DOCX originals.');
    console.log(`PASS: SQLite hybrid, vector-only and keyword-only retrieval returned both original citations; model stub handled ${embeddingCalls} embedding and ${chatCalls} LLM requests.`);
  } catch (error) {
    console.error(error.message);
    console.error(`Diagnostics retained in ${temp}`);
    process.exitCode = 1;
  } finally {
    await backend.stop();
    await new Promise(resolve => mock.close(resolve));
    if (!process.exitCode) await fs.rm(temp, { recursive: true, force: true });
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
