const API = '/api/v1';
let bots = [];
let agentCapabilities = [];
let selectedBotId = '';
let loginPollTimer = null;
let activeBindingId = '';

// ── Loading state ────────────────────────────────────────

function showLoading(elementId, count = 4) {
  const el = document.getElementById(elementId);
  if (!el) return;
  el.innerHTML = Array.from({ length: count }, (_, i) =>
    `<div class="skeleton${i === count - 1 ? ' short' : ''}"></div>`
  ).join('');
}

// ── Agent Capabilities ──────────────────────────────────

async function loadAgentCapabilities() {
  const data = await api('GET', '/agent-capabilities');
  if (data.code !== 'OK') {
    toast(data.message || data.code);
    return;
  }
  agentCapabilities = data.data || [];
  renderCapabilityOptions('create-bot-capability', 'create-bot-mode');
  renderSelectedBotAgentControls();
}

function renderCapabilityOptions(capabilitySelectId, modeSelectId, selectedCapabilityId, selectedMode) {
  const capabilitySelect = document.getElementById(capabilitySelectId);
  const modeSelect = document.getElementById(modeSelectId);
  if (!capabilitySelect || !modeSelect) return;
  capabilitySelect.innerHTML = '<option value="">Default</option>' +
    agentCapabilities.map(item => `<option value="${escapeHtml(item.id)}">${escapeHtml(item.label || item.key)}</option>`).join('');
  capabilitySelect.value = selectedCapabilityId || '';
  syncModeOptions(capabilitySelectId, modeSelectId, selectedMode);
}

function syncModeOptions(capabilitySelectId, modeSelectId, selectedMode) {
  const capabilitySelect = document.getElementById(capabilitySelectId);
  const modeSelect = document.getElementById(modeSelectId);
  if (!capabilitySelect || !modeSelect) return;
  const capability = agentCapabilities.find(item => item.id === capabilitySelect.value);
  const modes = capability?.supported_modes || [];
  modeSelect.innerHTML = '<option value="">Default</option>' +
    modes.map(mode => `<option value="${escapeHtml(mode)}">${escapeHtml(mode)}</option>`).join('');
  modeSelect.value = modes.includes(selectedMode) ? selectedMode : '';
}

// ── Bot Selection & Detail ──────────────────────────────

function selectedBot() {
  return bots.find(b => b.bot_id === selectedBotId);
}

function renderSelectedBotAgentControls() {
  const bot = selectedBot();
  renderCapabilityOptions('detail-agent-capability', 'detail-agent-mode', bot?.agent_capability_id || '', bot?.agent_mode || '');
}

async function saveSelectedBotAgent() {
  const bot = selectedBot();
  if (!bot) { toast('select a bot'); return; }
  const agentCapabilityID = document.getElementById('detail-agent-capability').value;
  const agentMode = document.getElementById('detail-agent-mode').value;
  if (!agentCapabilityID || !agentMode) { toast('capability and mode required'); return; }
  const data = await api('POST', '/bots/agent', {
    bot_id: bot.bot_id,
    agent_capability_id: agentCapabilityID,
    agent_mode: agentMode,
  });
  if (data.code !== 'OK') { toast(data.message || data.code); return; }
  const updated = data.data;
  bot.agent_capability_id = updated.agent_capability_id;
  bot.agent_mode = updated.agent_mode;
  renderSelectedBotAgentControls();
  renderBotList();
  renderDetail();
  toast('agent updated');
}

// ── Event listeners ─────────────────────────────────────

window.addEventListener('DOMContentLoaded', () => {
  document.getElementById('create-bot-capability').addEventListener('change', () => syncModeOptions('create-bot-capability', 'create-bot-mode', ''));
  document.getElementById('detail-agent-capability').addEventListener('change', () => syncModeOptions('detail-agent-capability', 'detail-agent-mode', ''));
});

// ── Toast ───────────────────────────────────────────────

function toast(msg) {
  const el = document.createElement('div');
  el.className = 'toast';
  el.textContent = msg;
  document.body.appendChild(el);
  setTimeout(() => el.remove(), 2400);
}

// ── Status badge ────────────────────────────────────────

function statusBadge(status) {
  const map = {
    login_required: 'warning',
    connecting: 'info',
    connected: 'success',
    error: 'danger',
    qr_ready: 'info',
    pending: 'neutral',
    confirmed: 'success',
    failed: 'danger',
    expired: 'warning'
  };
  return `<span class="badge badge-${map[status] || 'neutral'}">${escapeHtml(status)}</span>`;
}

// ── API helper ──────────────────────────────────────────

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(API + path, opts);
  return res.json();
}

// ── User ────────────────────────────────────────────────

function currentUserId() {
  return 'u_123';
}

// ── Create Bot Modal ────────────────────────────────────

function openCreateBotModal() {
  document.getElementById('create-bot-modal').classList.add('active');
}

function closeCreateBotModal() {
  document.getElementById('create-bot-modal').classList.remove('active');
}

async function createBot() {
  const userId = currentUserId();
  const name = document.getElementById('create-bot-name').value.trim();
  const channelType = document.getElementById('create-bot-channel').value;
  const agentCapabilityID = document.getElementById('create-bot-capability').value;
  const agentMode = document.getElementById('create-bot-mode').value;
  if (!userId || !name) { toast('user_id and name required'); return; }
  const data = await api('POST', '/bots/create', {
    user_id: userId,
    name,
    channel_type: channelType,
    agent_capability_id: agentCapabilityID,
    agent_mode: agentMode,
  });
  if (data.code !== 'OK') { toast(data.message || data.code); return; }
  document.getElementById('create-bot-name').value = '';
  document.getElementById('create-bot-capability').value = '';
  document.getElementById('create-bot-mode').innerHTML = '<option value="">Default</option>';
  closeCreateBotModal();
  await loadBots(data.data.bot_id);
}

// ── Load & Render Bots ──────────────────────────────────

async function loadBots(preferBotId) {
  const userId = currentUserId();
  if (!userId) { toast('user_id required'); return; }
  showLoading('bot-list', 4);
  const data = await api('GET', '/bots/list?user_id=' + encodeURIComponent(userId));
  if (data.code !== 'OK') { toast(data.message || data.code); return; }
  bots = data.data || [];
  if (preferBotId) {
    selectedBotId = preferBotId;
  } else if (!bots.some(b => b.bot_id === selectedBotId)) {
    selectedBotId = bots[0]?.bot_id || '';
  }
  renderBotList();
  document.getElementById('bot-count').textContent = `(${bots.length})`;
  renderDetail();
}

function statusDotClass(status) {
  const map = {
    connected: 'connected',
    connecting: 'connecting',
    error: 'error',
    login_required: 'login_required',
    qr_ready: 'qr_ready',
    pending: 'pending',
    confirmed: 'connected',
    failed: 'error',
    expired: 'pending'
  };
  return map[status] || 'default';
}

function renderBotList() {
  const list = document.getElementById('bot-list');
  const empty = document.getElementById('bot-empty');
  if (!bots.length) {
    list.innerHTML = '';
    empty.style.display = 'flex';
    return;
  }
  empty.style.display = 'none';
  list.innerHTML = bots.map(bot => `
    <div class="bot-item ${bot.bot_id === selectedBotId ? 'active' : ''}" onclick="selectBot('${bot.bot_id}')">
      <div class="bot-status-dot ${statusDotClass(bot.connection_status)}"></div>
      <div class="bot-info">
        <div class="bot-name">${escapeHtml(bot.name)}</div>
        <div class="bot-channel">${escapeHtml(bot.channel_type)}</div>
      </div>
      <button class="bot-delete" type="button" title="Delete" aria-label="Delete" onclick="event.stopPropagation(); deleteBot('${bot.bot_id}', '${escapeJs(bot.name)}')">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M19 6l-1 14H6L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/>
        </svg>
      </button>
    </div>
  `).join('');
}

function selectBot(botId) {
  selectedBotId = botId;
  renderBotList();
  renderDetail();
}

function renderDetail() {
  const empty = document.getElementById('workspace-empty');
  const detail = document.getElementById('workspace-detail');
  const bot = bots.find(b => b.bot_id === selectedBotId);
  if (!bot) {
    stopLoginPolling();
    empty.style.display = 'flex';
    return;
  }
  empty.style.display = 'none';
  detail.style.display = 'grid';
  document.getElementById('detail-name').textContent = bot.name;
  document.getElementById('detail-status-badge').innerHTML = statusBadge(bot.connection_status);
  document.getElementById('detail-channel').textContent = bot.channel_type;
  document.getElementById('detail-bot-id').textContent = bot.bot_id;
  document.getElementById('detail-account-id').textContent = bot.channel_account_id || '-';
  document.getElementById('detail-hook-url').textContent = hookUrl(bot.name);
  renderSelectedBotAgentControls();
  if (!activeBindingId) {
    document.getElementById('connect-result').innerHTML = '';
  }
}

// ── Login Polling ───────────────────────────────────────

function stopLoginPolling() {
  if (loginPollTimer) {
    clearTimeout(loginPollTimer);
    loginPollTimer = null;
  }
  activeBindingId = '';
}

function updateBotFromRefresh(result) {
  const bot = bots.find(item => item.bot_id === result.bot_id);
  if (!bot) return;
  bot.connection_status = result.connection_status;
  bot.channel_account_id = result.channel_account_id || '';
  renderBotList();
  renderDetail();
}

async function pollLogin(bindingID) {
  const data = await api('GET', '/bots/connect?binding_id=' + encodeURIComponent(bindingID));
  if (data.code !== 'OK') {
    stopLoginPolling();
    toast(data.message || data.code);
    return;
  }
  const result = data.data;
  activeBindingId = result.binding_id;
  document.getElementById('connect-result').innerHTML = '';
  if (result.qr_code_payload) {
    showQRModal(result.qr_code_payload, result.qr_share_url, result.status);
  }
  updateBotFromRefresh(result);
  if (result.status === 'confirmed') {
    closeQRModal();
    stopLoginPolling();
    toast('connected');
    return;
  }
  if (result.status === 'failed' || result.status === 'expired' || result.connection_status === 'error') {
    stopLoginPolling();
    toast(result.status);
    return;
  }
  loginPollTimer = setTimeout(() => pollLogin(bindingID), 2000);
}

function startLoginPolling(bindingID) {
  stopLoginPolling();
  activeBindingId = bindingID;
  loginPollTimer = setTimeout(() => pollLogin(bindingID), 2000);
}

// ── Connect ─────────────────────────────────────────────

async function connectSelectedBot() {
  if (!selectedBotId) { toast('select a bot'); return; }
  const data = await api('POST', '/bots/connect', { bot_id: selectedBotId });
  if (data.code !== 'OK') { toast(data.message || data.code); return; }
  const result = data.data;
  activeBindingId = result.binding_id;
  document.getElementById('connect-result').innerHTML = '';
  if (result.qr_code_payload) {
    showQRModal(result.qr_code_payload, result.qr_share_url, result.status);
  }
  startLoginPolling(result.binding_id);
}

// ── Delete Bot ──────────────────────────────────────────

async function deleteBot(botID, botName) {
  const name = botName || botID;
  if (!confirm(`Delete bot "${name}"?`)) return;
  if (botID === selectedBotId) {
    stopLoginPolling();
    closeQRModal();
  }
  const data = await api('POST', '/bots/delete', { bot_id: botID });
  if (data.code !== 'OK') { toast(data.message || data.code); return; }
  toast('bot deleted');
  await loadBots();
}

// ── QR Modal ────────────────────────────────────────────

function closeQRModal() {
  document.getElementById('qr-modal').classList.remove('active');
}

async function copyShareURL() {
  const link = document.getElementById('qr-share-link').href;
  if (!link) return;
  await navigator.clipboard.writeText(link);
  toast('link copied');
}

function showQRModal(payload, shareURL, status) {
  const image = document.getElementById('qr-image');
  const shareBox = document.getElementById('qr-share-box');
  const shareLink = document.getElementById('qr-share-link');
  if (payload) {
    image.src = payload;
    image.style.display = 'block';
  } else {
    image.removeAttribute('src');
    image.style.display = 'none';
  }
  if (shareURL) {
    shareLink.href = shareURL;
    shareLink.textContent = shareURL;
    shareBox.style.display = 'block';
  } else {
    shareLink.removeAttribute('href');
    shareLink.textContent = '';
    shareBox.style.display = 'none';
  }
  document.getElementById('qr-status-text').textContent = status || 'qr_ready';
  document.getElementById('qr-modal').classList.add('active');
}

// ── HTML escaping ───────────────────────────────────────

function escapeHtml(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function escapeJs(value) {
  return String(value ?? '')
    .replaceAll('\\', '\\\\')
    .replaceAll("'", "\\'");
}

// ── Hook URL ──────────────────────────────────────────────

function hookUrl(botName) {
  return window.location.origin + '/hooks/' + encodeURIComponent(botName);
}

function copyHookUrl() {
  const bot = selectedBot();
  if (!bot) { toast('select a bot'); return; }
  const url = hookUrl(bot.name);
  navigator.clipboard.writeText(url).then(() => toast('hook url copied')).catch(() => toast('copy failed'));
}

// ── Bootstrap ───────────────────────────────────────────

loadAgentCapabilities().then(() => loadBots());
