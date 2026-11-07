//go:build windows

package ui

import (
	"strings"

	"wenchang-campus-keeper/internal/config"
)

func GetAppHTML() string {
	return strings.ReplaceAll(appHTML, "{{VERSION}}", config.AppVersion)
}

const appHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>CampusKeeper v{{VERSION}}</title>
<style>
  * {
    box-sizing: border-box;
    margin: 0;
    padding: 0;
    user-select: none;
    -webkit-user-select: none;
  }
  :root {
    --bg-window: #f3f3f3;
    --card-bg: #ffffff;
    --text-primary: #1b1b1b;
    --text-secondary: #5c5c5c;
    --text-muted: #767676;
    --border-card: #e5e5e5;
    --border-control: #d1d1d1;
    --border-control-hover: #868686;
    --accent: #0067c0;
    --accent-hover: #1873c4;
    --accent-active: #005a9e;
    --accent-text: #ffffff;
    --status-online: #107c41;
    --status-offline: #797775;
    --status-busy: #b45309;
  }

  html, body {
    width: 100%;
    height: 100%;
    margin: 0;
    padding: 0;
    overflow: hidden;
    background: var(--bg-window);
    font-family: "Segoe UI Variable Text", "Segoe UI", -apple-system, "Microsoft YaHei UI", sans-serif;
    color: var(--text-primary);
    font-size: 13px;
    line-height: 1.5;
  }

  .window-wrapper {
    width: 100%;
    height: 100%;
    padding: 14px 18px;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  /* Top Navigation & Status Bar */
  .top-nav-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 34px;
    margin-bottom: 12px;
    flex-shrink: 0;
  }
  .segmented-nav {
    display: flex;
    background: #e5e5e5;
    padding: 3px;
    border-radius: 6px;
    height: 32px;
    width: 330px;
    flex-shrink: 0;
  }
  .nav-item {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 500;
    color: var(--text-secondary);
    border: none;
    background: transparent;
    cursor: pointer;
    border-radius: 4px;
    transition: all 0.15s ease;
  }
  .nav-item.active {
    background: #ffffff;
    color: var(--text-primary);
    font-weight: 600;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
  }
  .status-indicator {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: var(--text-secondary);
    background: #ffffff;
    border: 1px solid var(--border-card);
    padding: 4px 12px;
    border-radius: 12px;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
  }
  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background-color: var(--status-online);
  }
  .status-indicator.offline .status-dot {
    background-color: var(--status-offline);
  }
  .status-indicator.busy .status-dot {
    background-color: var(--status-busy);
  }

  /* Views */
  .view-panel {
    display: none;
    flex-direction: column;
    flex: 1;
    overflow-y: auto;
    overflow-x: hidden;
  }
  .view-panel.active {
    display: flex;
  }

  /* Fluent Cards */
  .settings-container {
    width: 100%;
    max-width: 580px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .fluent-card {
    background: var(--card-bg);
    border: 1px solid var(--border-card);
    border-radius: 8px;
    overflow: hidden;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.03);
  }
  .card-header {
    padding: 10px 16px;
    font-size: 12.5px;
    font-weight: 600;
    background: #fafafa;
    border-bottom: 1px solid #f0f0f0;
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
  .setting-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 16px;
    height: 40px;
    border-bottom: 1px solid #f2f2f2;
  }
  .setting-row:last-child {
    border-bottom: none;
  }
  .setting-label {
    font-size: 13px;
    font-weight: 500;
    color: var(--text-primary);
    width: 110px;
    flex-shrink: 0;
  }
  .control-wrapper {
    flex: 1;
    display: flex;
    justify-content: flex-end;
    align-items: center;
  }
  .fluent-input {
    width: 270px;
    height: 30px;
    background: #ffffff;
    border: 1px solid var(--border-control);
    border-bottom: 1px solid #999999;
    border-radius: 4px;
    padding: 0 10px;
    font-size: 12.5px;
    color: var(--text-primary);
    font-family: inherit;
    outline: none;
    user-select: text;
    -webkit-user-select: text;
    transition: all 0.1s ease;
  }
  .fluent-input:focus {
    border-color: var(--accent);
    border-bottom-width: 2px;
  }

  .mode-radio-group {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px 16px;
  }
  .mode-option {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    cursor: pointer;
    padding: 8px 10px;
    border-radius: 6px;
    border: 1px solid transparent;
    transition: all 0.1s;
  }
  .mode-option:hover {
    background: #f7f7f7;
  }
  .mode-option.selected {
    background: #edf5fc;
    border-color: #b9d8f3;
  }
  .mode-desc {
    font-size: 11.5px;
    color: var(--text-muted);
    margin-top: 2px;
  }

  .info-bar {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 10px 16px;
    background: #fafafa;
    border-top: 1px solid #f2f2f2;
    font-size: 12px;
    color: var(--text-secondary);
  }
  .info-val {
    font-weight: 500;
    color: var(--text-primary);
  }

  /* Buttons */
  .fluent-btn {
    height: 32px;
    padding: 0 16px;
    font-size: 12.5px;
    font-weight: 500;
    border-radius: 4px;
    border: 1px solid transparent;
    cursor: pointer;
    font-family: inherit;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    transition: all 0.1s ease;
  }
  .fluent-btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }
  .fluent-btn-primary {
    background: var(--accent);
    color: var(--accent-text);
  }
  .fluent-btn-primary:not(:disabled):hover {
    background: var(--accent-hover);
  }
  .fluent-btn-secondary {
    background: #ffffff;
    color: var(--text-primary);
    border-color: var(--border-control);
    border-bottom-color: #999999;
  }
  .fluent-btn-secondary:not(:disabled):hover {
    background: #fbfbfb;
  }
  .fluent-btn-danger {
    background: #ffffff;
    color: #d13438;
    border-color: #f1c5c6;
  }
  .fluent-btn-danger:hover {
    background: #fdf3f4;
  }

  .mini-btn {
    height: 26px;
    padding: 0 10px;
    font-size: 11.5px;
    background: #ffffff;
    border: 1px solid var(--border-control);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-family: inherit;
  }
  .mini-btn:hover {
    background: #f7f7f7;
    color: var(--text-primary);
  }

  .footer-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 6px;
  }

  /* Share list */
  .share-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .share-table th, .share-table td {
    padding: 8px 12px;
    text-align: left;
    border-bottom: 1px solid #f0f0f0;
  }
  .share-table th {
    background: #fcfcfc;
    color: var(--text-muted);
    font-weight: 500;
  }

  /* Logs Panel */
  .log-view {
    height: 100%;
    display: flex;
    flex-direction: column;
    gap: 8px;
    overflow: hidden;
  }
  .log-top {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
  .console-box {
    flex: 1;
    width: 100%;
    min-height: 240px;
    background: #181818;
    border: 1px solid #2d2d2d;
    border-radius: 6px;
    padding: 10px 12px;
    color: #cccccc;
    font-family: "Cascadia Code", "Consolas", monospace;
    font-size: 11px;
    line-height: 1.42;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-all;
    user-select: text;
    -webkit-user-select: text;
  }

  /* Toast */
  .toast {
    position: fixed;
    bottom: 20px;
    left: 50%;
    transform: translateX(-50%) translateY(30px);
    background: rgba(32, 32, 32, 0.95);
    color: #ffffff;
    padding: 8px 18px;
    border-radius: 4px;
    font-size: 12px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
    opacity: 0;
    transition: all 0.2s ease;
    pointer-events: none;
    z-index: 100;
  }
  .toast.show {
    transform: translateX(-50%) translateY(0);
    opacity: 1;
  }
</style>
</head>
<body>

<div class="window-wrapper">
  <!-- Top Navigation & Status -->
  <div class="top-nav-bar">
    <div class="segmented-nav">
      <button class="nav-item active" data-tab="dashboard">控制面板</button>
      <button class="nav-item" data-tab="account">我的账号</button>
      <button class="nav-item" data-tab="sharing">好友共享</button>
      <button class="nav-item" data-tab="logs">系统日志</button>
    </div>
    <div id="statusBadge" class="status-indicator">
      <span class="status-dot"></span>
      <span id="badgeText">检测中</span>
    </div>
  </div>

  <!-- VIEW 1: Dashboard & Mode Selection -->
  <div id="view-dashboard" class="view-panel active">
    <div class="settings-container">
      <div class="fluent-card">
        <div class="card-header">
          <span>网络连接与登录模式</span>
          <span id="activeModeBadge" style="color: var(--accent); font-weight: 500;">自身账号</span>
        </div>
        <div class="mode-radio-group">
          <label class="mode-option selected" id="optSelf">
            <input type="radio" name="activeMode" value="self" checked style="margin-top: 3px;" />
            <div>
              <div style="font-weight: 600;">使用我的账号（本地直连）</div>
              <div class="mode-desc">直接读取本地加密保存的校园网账号，秒级离线认证，不受云端限制。</div>
            </div>
          </label>
          <label class="mode-option" id="optFriend">
            <input type="radio" name="activeMode" value="friend" style="margin-top: 3px;" />
            <div>
              <div style="font-weight: 600;">借用好友账号（Cloudflare 云端鉴权）</div>
              <div class="mode-desc">使用好友生成的专属分享码，通过 Cloudflare 安全拉取密文登录，所有者可随时吊销。</div>
            </div>
          </label>
        </div>

        <!-- Friend Share Code input row (only visible in Friend mode) -->
        <div id="friendInputSection" style="display: none; padding: 10px 16px; background: #fafafa; border-top: 1px solid #f0f0f0;">
          <div style="display: flex; gap: 8px; align-items: center;">
            <input type="text" id="friendShareCode" class="fluent-input" style="flex: 1;" placeholder="粘贴好友分享码 (CK1.sh_xxxx...)" autocomplete="off" />
            <button id="btnApplyFriendMode" class="fluent-btn fluent-btn-primary" style="height: 30px;">应用借用</button>
          </div>
          <div style="font-size: 11px; color: var(--text-muted); margin-top: 4px;">* 凭据仅在内存中临时解密发包，绝不会在本地明文留存</div>
        </div>

        <div class="info-bar">
          <span>当前网络链路</span>
          <span id="linkInfo" class="info-val">检测中...</span>
        </div>
      </div>

      <div class="footer-actions">
        <button id="btnExit" class="fluent-btn fluent-btn-danger" style="margin-right: auto;">退出程序</button>
        <button id="btnReconnect" class="fluent-btn fluent-btn-secondary">立即重新连接</button>
      </div>
    </div>
  </div>

  <!-- VIEW 2: My Account -->
  <div id="view-account" class="view-panel">
    <div class="settings-container">
      <div class="fluent-card">
        <div class="card-header">
          <span>我的校园网凭据配置</span>
          <span style="font-size: 11px; color: var(--text-muted);">Windows DPAPI 本地硬件加密</span>
        </div>
        <div class="setting-row">
          <label class="setting-label" for="account">学号 / 工号</label>
          <div class="control-wrapper">
            <input type="text" id="account" class="fluent-input" placeholder="学号 / 工号" autocomplete="off" spellcheck="false" />
          </div>
        </div>
        <div class="setting-row">
          <label class="setting-label" for="password">校园网密码</label>
          <div class="control-wrapper">
            <input type="password" id="password" class="fluent-input" placeholder="校园网密码" autocomplete="off" />
          </div>
        </div>
        <div class="setting-row">
          <label class="setting-label" for="operator">运营商</label>
          <div class="control-wrapper">
            <select id="operator" class="fluent-input">
              <option value="" disabled selected hidden>请选择运营商</option>
              <option value="移动">中国移动</option>
              <option value="电信">中国电信</option>
              <option value="联通">中国联通</option>
              <option value="校园网">校园网</option>
            </select>
          </div>
        </div>
      </div>

      <div class="footer-actions">
        <button id="btnSaveAccount" class="fluent-btn fluent-btn-primary">保存账号配置</button>
      </div>
    </div>
  </div>

  <!-- VIEW 3: Sharing Hub -->
  <div id="view-sharing" class="view-panel">
    <div class="settings-container">
      <!-- Create Share Card -->
      <div class="fluent-card">
        <div class="card-header">
          <span>借给好友（创建专属分享码）</span>
          <span style="font-size: 11px; color: var(--text-muted);">Cloudflare Workers + KV 托管</span>
        </div>
        <div class="setting-row">
          <label class="setting-label" for="shareMemo">好友备注</label>
          <div class="control-wrapper">
            <input type="text" id="shareMemo" class="fluent-input" placeholder="例如：室友老王的笔记本" autocomplete="off" />
          </div>
        </div>
        <div class="setting-row">
          <label class="setting-label" for="shareDuration">有效时长</label>
          <div class="control-wrapper">
            <select id="shareDuration" class="fluent-input">
              <option value="24">24 小时 (临时借用)</option>
              <option value="168" selected>7 天 (一整周)</option>
              <option value="720">30 天 (整月)</option>
              <option value="8760">1 年 (长期互助)</option>
            </select>
          </div>
        </div>
        <div id="newShareResultRow" style="display: none; padding: 10px 16px; background: #edf5fc; border-top: 1px solid #d3e5f5;">
          <div style="font-size: 11.5px; font-weight: 600; color: var(--accent); margin-bottom: 4px;">分享码已生成，点击复制发给好友：</div>
          <div style="display: flex; gap: 8px;">
            <input type="text" id="createdShareCode" class="fluent-input" style="flex: 1; font-family: monospace;" readonly />
            <button id="btnCopyShareCode" class="fluent-btn fluent-btn-primary" style="height: 30px;">复制</button>
          </div>
        </div>
        <div style="padding: 10px 16px; display: flex; justify-content: flex-end;">
          <button id="btnCreateShare" class="fluent-btn fluent-btn-primary">生成分享码</button>
        </div>
      </div>

      <!-- Manage Shares Card -->
      <div class="fluent-card">
        <div class="card-header">
          <span>我已借出的分享管理</span>
          <button id="btnRefreshShares" class="mini-btn">刷新云端列表</button>
        </div>
        <div style="max-height: 200px; overflow-y: auto;">
          <table class="share-table">
            <thead>
              <tr>
                <th>备注</th>
                <th>运营商</th>
                <th>到期时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody id="shareTableBody">
              <tr>
                <td colspan="4" style="text-align: center; color: var(--text-muted); padding: 16px;">暂无活跃的分享</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>

  <!-- VIEW 4: Logs -->
  <div id="view-logs" class="view-panel">
    <div class="log-view">
      <div class="log-top">
        <button id="btnClear" class="mini-btn">清空</button>
        <button id="btnOpenDir" class="mini-btn">数据目录</button>
      </div>
      <pre id="logConsole" class="console-box"></pre>
    </div>
  </div>
</div>

<div id="toast" class="toast">
  <span id="toastMsg">提示内容</span>
</div>

<script>
  // Tab Switching
  function switchTab(name) {
    const tabs = document.querySelectorAll('.nav-item');
    const panels = document.querySelectorAll('.view-panel');
    tabs.forEach(t => t.classList.toggle('active', t.getAttribute('data-tab') === name));
    panels.forEach(p => p.classList.toggle('active', p.id === 'view-' + name));
    if (name === 'logs') {
      const el = document.getElementById('logConsole');
      if (el) el.scrollTop = el.scrollHeight;
    }
    if (name === 'sharing') {
      loadMyShares();
    }
  }
  window.switchTab = switchTab;

  document.querySelectorAll('.nav-item').forEach(btn => {
    btn.addEventListener('click', () => switchTab(btn.getAttribute('data-tab')));
  });

  // Toast Helper
  let toastTimer = null;
  function showToast(msg) {
    const toast = document.getElementById('toast');
    document.getElementById('toastMsg').innerText = msg;
    toast.classList.add('show');
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toast.classList.remove('show'), 2200);
  }

  // Dashboard Status Sync
  window.onStatusUpdate = function(dash) {
    if (!dash) return;
    const badge = document.getElementById('statusBadge');
    const badgeText = document.getElementById('badgeText');
    const linkInfo = document.getElementById('linkInfo');

    const status = dash.status || '未连接';
    badgeText.innerText = status;

    badge.className = 'status-indicator';
    if (status.includes('正常') || status.includes('已连接') || status.includes('在线') || status.includes('已连通')) {
      // online
    } else if (status.includes('重试') || status.includes('中') || status.includes('正在') || status.includes('检测')) {
      badge.classList.add('busy');
    } else {
      badge.classList.add('offline');
    }

    const net = dash.network || '未就绪';
    const ip = dash.ip || '无 IP';
    linkInfo.innerText = net + ' · ' + ip;
  };

  // Logs stream
  const logConsole = document.getElementById('logConsole');
  window.onNewLog = function(chunk) {
    logConsole.innerText += chunk;
    if (logConsole.innerText.length > 50000) {
      logConsole.innerText = logConsole.innerText.slice(-35000);
    }
    logConsole.scrollTop = logConsole.scrollHeight;
  };

  document.getElementById('btnClear').addEventListener('click', () => {
    logConsole.innerText = '';
    showToast('日志已清空');
  });

  document.getElementById('btnOpenDir').addEventListener('click', () => {
    if (window.openDataDir) window.openDataDir();
  });

  document.getElementById('btnReconnect').addEventListener('click', async () => {
    if (window.reconnect) {
      await window.reconnect();
      showToast('已请求重新检测连接');
    }
  });

  document.getElementById('btnExit').addEventListener('click', async () => {
    if (confirm('确定要退出校园网守护程序吗？退出后将停止自动保活。')) {
      if (window.exitApp) await window.exitApp();
    }
  });

  // Mode Selection Interaction
  function updateModeSelection(mode) {
    const isSelf = mode === 'self';
    document.getElementById('optSelf').classList.toggle('selected', isSelf);
    document.getElementById('optFriend').classList.toggle('selected', !isSelf);
    document.getElementById('friendInputSection').style.display = isSelf ? 'none' : 'block';
    document.getElementById('activeModeBadge').innerText = isSelf ? '自身账号 (本地)' : '借用好友 (云端)';
    document.getElementById('activeModeBadge').style.color = isSelf ? 'var(--accent)' : '#b45309';
  }

  document.querySelectorAll('input[name="activeMode"]').forEach(radio => {
    radio.addEventListener('change', (e) => {
      const mode = e.target.value;
      updateModeSelection(mode);
      if (mode === 'self') {
        if (window.switchMode) {
          window.switchMode('self', '').then(() => showToast('已切换至自身账号登录'));
        }
      }
    });
  });

  document.getElementById('btnApplyFriendMode').addEventListener('click', async () => {
    const code = document.getElementById('friendShareCode').value.trim();
    if (!code) {
      showToast('请输入好友分享码');
      return;
    }
    try {
      if (window.switchMode) {
        await window.switchMode('friend', code);
        showToast('已切换至借用模式，正在连接云端鉴权...');
      }
    } catch(err) {
      alert('切换失败: ' + err);
    }
  });

  // Account Saving
  let savedAccount = '';
  let hasSavedPassword = false;

  function updatePasswordPlaceholder() {
    const curAcc = document.getElementById('account').value.trim();
    const pwdEl = document.getElementById('password');
    const isDifferent = Boolean(savedAccount && curAcc.toLowerCase() !== savedAccount.toLowerCase());
    if (!hasSavedPassword || isDifferent) {
      pwdEl.placeholder = (savedAccount && isDifferent) ? '请输入新密码' : '校园网密码';
    } else {
      pwdEl.placeholder = '●●●●●●●● (留空保持原密码)';
    }
  }
  document.getElementById('account').addEventListener('input', updatePasswordPlaceholder);

  document.getElementById('btnSaveAccount').addEventListener('click', async () => {
    const account = document.getElementById('account').value.trim();
    const password = document.getElementById('password').value;
    const operator = document.getElementById('operator').value;
    if (!account) {
      showToast('请输入账号');
      return;
    }
    if (!operator) {
      showToast('请选择运营商');
      return;
    }
    try {
      if (window.saveConfig) {
        const changed = await window.saveConfig(account, password, operator);
        savedAccount = account;
        hasSavedPassword = hasSavedPassword || Boolean(password);
        document.getElementById('password').value = '';
        updatePasswordPlaceholder();
        showToast(changed ? '账号配置已保存，正在重新核验' : '配置未变更');
      }
    } catch(err) {
      alert('保存失败: ' + err);
    }
  });

  // Sharing Hub
  document.getElementById('btnCreateShare').addEventListener('click', async () => {
    const memo = document.getElementById('shareMemo').value.trim() || '好友分享';
    const hours = parseInt(document.getElementById('shareDuration').value, 10) || 168;
    try {
      if (window.publishShare) {
        const code = await window.publishShare(memo, hours);
        document.getElementById('createdShareCode').value = code;
        document.getElementById('newShareResultRow').style.display = 'block';
        showToast('分享码创建成功');
        loadMyShares();
      }
    } catch(err) {
      alert('生成分享码失败: ' + err);
    }
  });

  document.getElementById('btnCopyShareCode').addEventListener('click', () => {
    const input = document.getElementById('createdShareCode');
    input.select();
    navigator.clipboard.writeText(input.value).then(() => {
      showToast('分享码已复制到剪贴板！');
    });
  });

  async function loadMyShares() {
    if (!window.listMyShares) return;
    try {
      const shares = await window.listMyShares();
      const tbody = document.getElementById('shareTableBody');
      if (!shares || !shares.length) {
        tbody.innerHTML = '<tr><td colspan="4" style="text-align: center; color: var(--text-muted); padding: 16px;">暂无活跃的分享</td></tr>';
        return;
      }
      tbody.innerHTML = shares.map(s => {
        const exp = new Date(s.expiresAt).toLocaleDateString();
        return '<tr>' +
          '<td><b>' + (s.memo || '未命名') + '</b></td>' +
          '<td>' + (s.operator || '移动') + '</td>' +
          '<td>' + exp + '</td>' +
          '<td><button class="mini-btn" style="color:#d13438;" onclick="revokeShareItem(\'' + s.shareId + '\')">吊销</button></td>' +
        '</tr>';
      }).join('');
    } catch(err) {
      console.error('加载分享列表失败:', err);
    }
  }
  document.getElementById('btnRefreshShares').addEventListener('click', loadMyShares);

  window.revokeShareItem = async function(shareId) {
    if (!confirm('确定要吊销此分享吗？吊销后对方将立即无法继续使用你的账号联网。')) return;
    try {
      if (window.revokeShare) {
        await window.revokeShare(shareId);
        showToast('已成功吊销该分享');
        loadMyShares();
      }
    } catch(err) {
      alert('吊销失败: ' + err);
    }
  };

  // Initial Data Load
  (async function init() {
    try {
      if (window.getConfig) {
        const cfg = await window.getConfig();
        document.getElementById('account').value = cfg.account || '';
        document.getElementById('operator').value = cfg.operator || '移动';
        document.getElementById('friendShareCode').value = cfg.friendShareCode || '';
        savedAccount = cfg.account || '';
        hasSavedPassword = Boolean(cfg.hasPassword);
        updatePasswordPlaceholder();

        const mode = cfg.activeMode || 'self';
        const radio = document.querySelector('input[name="activeMode"][value="' + mode + '"]');
        if (radio) radio.checked = true;
        updateModeSelection(mode);
      }
      if (window.getDashboard) {
        const d = await window.getDashboard();
        window.onStatusUpdate(d);
      }
      if (window.getLogs) {
        const logs = await window.getLogs();
        if (logs) logConsole.innerText = logs;
        logConsole.scrollTop = logConsole.scrollHeight;
      }
      if (window.onUIRendered) {
        await window.onUIRendered();
      }
    } catch(e) {
      console.error('UI init error:', e);
    }
  })();
</script>
</body>
</html>`
