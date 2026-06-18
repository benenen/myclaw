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

function botType() {
  const el = document.querySelector('input[name="bot-type"]:checked');
  return el ? el.value : 'channel';
}

function toggleBotType() {
  const channelField = document.getElementById('create-bot-channel-field');
  const nameInput = document.getElementById('create-bot-name');
  if (botType() === 'hook') {
    channelField.style.display = 'none';
    nameInput.placeholder = 'e.g. vikunja';
  } else {
    channelField.style.display = 'block';
    nameInput.placeholder = 'e.g. sales-bot';
  }
}

function openCreateBotModal() {
  toggleBotType();
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
  const body = {
    user_id: userId,
    name,
    type: botType(),
    agent_capability_id: agentCapabilityID || undefined,
    agent_mode: agentMode || undefined,
  };
  if (botType() === 'channel') {
    body.channel_type = channelType;
  }
  const data = await api('POST', '/bots/create', body);
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
    detail.style.display = 'none';
    return;
  }
  empty.style.display = 'none';
  detail.style.display = 'grid';

  // A WeChat channel bot connects via QR login; an HTTP channel bot auto-connects.
  const isHook = bot.type === 'hook';
  const isWeChatChannel = bot.type === 'channel' && bot.channel_type === 'wechat';
  const isHttpChannel = bot.type === 'channel' && bot.channel_type === 'http';
  const isFeishuChannel = bot.type === 'channel' && bot.channel_type === 'feishu';

  document.getElementById('detail-name').textContent = bot.name;
  document.getElementById('detail-status-badge').innerHTML = statusBadge(bot.connection_status);
  document.getElementById('detail-channel').textContent = bot.channel_type || (isHook ? 'webhook' : '-');
  document.getElementById('detail-bot-id').textContent = bot.bot_id;
  document.getElementById('detail-account-id').textContent = bot.channel_account_id || '-';
  document.getElementById('detail-hook-url').textContent = hookUrl(bot.name);

  // Only hook bots expose a webhook; HTTP channel bots show their API endpoint.
  document.getElementById('detail-webhook-card').style.display = isHook ? '' : 'none';
  document.getElementById('detail-http-channel-card').style.display = isHttpChannel ? '' : 'none';
  if (isHttpChannel) {
    document.getElementById('detail-http-channel-url').textContent = httpChannelUrl();
  }

  // Connect card: WeChat (QR), HTTP (auto-confirm), and Feishu (auto-confirm)
  const showConnect = isWeChatChannel || isHttpChannel || isFeishuChannel;
  const connected = bot.connection_status === 'connected';
  document.getElementById('detail-connect-card').style.display = (showConnect && !connected) ? '' : 'none';
  document.getElementById('detail-feishu-fields').style.display = isFeishuChannel ? '' : 'none';
  if (isHttpChannel) {
    document.getElementById('detail-connect-hint').textContent = 'Connect this bot to start chatting.';
  } else if (isWeChatChannel) {
    document.getElementById('detail-connect-hint').textContent = 'Generate a WeChat login QR and link this bot to an account.';
  } else if (isFeishuChannel) {
    document.getElementById('detail-connect-hint').textContent = 'Enter your Feishu self-built app credentials (App ID + App Secret) to connect.';
  }

  // Chat card: HTTP bot only, when connected (runtime active)
  document.getElementById('detail-chat-card').style.display = (isHttpChannel && connected) ? '' : 'none';
  if (isHttpChannel && connected) {
    initChatForBot(bot.bot_id);
  }

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
  const bot = selectedBot();
  const body = { bot_id: selectedBotId };
  if (bot && bot.channel_type === 'feishu') {
    const appId = document.getElementById('feishu-app-id').value.trim();
    const appSecret = document.getElementById('feishu-app-secret').value.trim();
    if (!appId || !appSecret) { toast('app_id and app_secret required'); return; }
    body.app_id = appId;
    body.app_secret = appSecret;
  }
  const data = await api('POST', '/bots/connect', body);
  if (data.code !== 'OK') { toast(data.message || data.code); return; }
  const result = data.data;
  activeBindingId = result.binding_id;
  document.getElementById('connect-result').replaceChildren();

  // HTTP and Feishu channels auto-confirm — reload immediately.
  if (bot && bot.type === 'channel' && (bot.channel_type === 'http' || bot.channel_type === 'feishu')) {
    await loadBots();
    return;
  }

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
  return window.location.origin + '/hooks/{platform}/' + encodeURIComponent(botName);
}

function copyHookUrl() {
  const bot = selectedBot();
  if (!bot) { toast('select a bot'); return; }
  const url = hookUrl(bot.name);
  navigator.clipboard.writeText(url).then(() => toast('hook url copied')).catch(() => toast('copy failed'));
}

// ── HTTP Channel URL ───────────────────────────────────────

function httpChannelUrl() {
  return window.location.origin + '/api/v1/channels/http/messages';
}

function copyHttpChannelUrl() {
  navigator.clipboard.writeText(httpChannelUrl()).then(() => toast('http channel url copied')).catch(() => toast('copy failed'));
}

// ── HTTP Channel Chat ─────────────────────────────────────

let chatBotId = '';
let chatSending = false;

function initChatForBot(botId) {
  if (chatBotId === botId) return;
  chatBotId = botId;
  const container = document.getElementById('chat-messages');
  container.innerHTML = '<div class="chat-msg notice">Chat with <code>' + escapeHtml(botId) + '</code></div>';
  document.getElementById('chat-input').value = '';
}

function handleChatKeydown(event) {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault();
    sendChatMessage();
  }
}

async function sendChatMessage() {
  if (chatSending) return;
  const input = document.getElementById('chat-input');
  const text = input.value.trim();
  if (!text || !chatBotId) return;

  chatSending = true;
  input.value = '';
  input.disabled = true;

  appendChatBubble('user', text);

  try {
    const data = await api('POST', '/channels/http/chat', {
      bot_id: chatBotId,
      text: text,
    });
    if (data.code === 'OK' && data.data && data.data.text) {
      appendChatBubble('bot', data.data.text, true);
    } else if (data.code === 'TIMEOUT') {
      appendChatBubble('bot', '⏱ 处理超时，请重试。');
    } else {
      appendChatBubble('bot', '❌ ' + (data.message || data.code || 'error'));
    }
  } catch (e) {
    appendChatBubble('bot', '❌ 网络错误：' + e.message);
  } finally {
    chatSending = false;
    input.disabled = false;
    input.focus();
  }
}

function appendChatBubble(role, text, markdown) {
  const container = document.getElementById('chat-messages');
  const div = document.createElement('div');
  div.className = 'chat-bubble ' + role;
  if (markdown) {
    div.innerHTML = renderMarkdown(text);
  } else {
    div.textContent = text;
  }
  container.appendChild(div);
  container.scrollTop = container.scrollHeight;
}

// ── Lightweight Markdown renderer ─────────────────────────

function renderMarkdown(text) {
  let html = escapeHtml(text);

  // Code blocks (``` ... ```)
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, function (_, lang, code) {
    return '<pre><code class="language-' + escapeHtml(lang) + '">' + code.trim() + '</code></pre>';
  });

  // Inline code (`...`)
  html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

  // Bold (**...**)
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');

  // Italic (*...*)
  html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');

  // Headers (### ...)
  html = html.replace(/^### (.+)$/gm, '<h4>$1</h4>');
  html = html.replace(/^## (.+)$/gm, '<h3>$1</h3>');
  html = html.replace(/^# (.+)$/gm, '<h2>$1</h2>');

  // Unordered list items (- ... or * ...)
  html = html.replace(/^[\-\*] (.+)$/gm, '<li>$1</li>');

  // Ordered list items (1. ...)
  html = html.replace(/^\d+\.\s+(.+)$/gm, '<li>$1</li>');

  // Wrap consecutive <li> in <ul>
  html = html.replace(/((?:<li>.*<\/li>\n?)+)/g, '<ul>$1</ul>');

  // Links [text](url)
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');

  // Horizontal rule (--- or ***)
  html = html.replace(/^(---|\*\*\*)$/gm, '<hr>');

  // Double newlines → paragraph breaks
  html = html.replace(/\n\n/g, '</p><p>');
  html = '<p>' + html + '</p>';

  // Clean up empty paragraphs
  html = html.replace(/<p>\s*<\/p>/g, '');
  html = html.replace(/<p>(<ul>)/g, '$1');
  html = html.replace(/(<\/ul>)<\/p>/g, '$1');

  return html;
}

// ── Bootstrap ───────────────────────────────────────────

loadAgentCapabilities().then(() => loadBots());
