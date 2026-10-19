//go:build windows

package main

const appHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>CampusKeeper</title>
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
    padding: 16px 22px;
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
    margin-bottom: 14px;
    flex-shrink: 0;
  }
  .segmented-nav {
    display: flex;
    background: #e5e5e5;
    padding: 3px;
    border-radius: 6px;
    height: 32px;
    width: 220px;
    flex-shrink: 0;
  }
  .nav-item {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12.5px;
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
    overflow: hidden;
  }
  .view-panel.active {
    display: flex;
  }

  /* Fluent Settings Card */
  .settings-container {
    width: 100%;
    max-width: 540px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .fluent-card {
    background: var(--card-bg);
    border: 1px solid var(--border-card);
    border-radius: 8px;
    overflow: hidden;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.03);
  }
  .setting-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 16px;
    height: 38px;
    border-bottom: 1px solid #f2f2f2;
  }
  .setting-row:last-child {
    border-bottom: none;
  }
  .setting-label {
    font-size: 13px;
    font-weight: 500;
    color: var(--text-primary);
    width: 100px;
    flex-shrink: 0;
  }
  .control-wrapper {
    flex: 1;
    display: flex;
    justify-content: flex-end;
    align-items: center;
    position: relative;
  }
  .fluent-input {
    width: 260px;
    height: 32px;
    background: #ffffff;
    border: 1px solid var(--border-control);
    border-bottom: 1px solid #999999;
    border-radius: 4px;
    padding: 0 10px;
    font-size: 13px;
    color: var(--text-primary);
    font-family: inherit;
    outline: none;
    user-select: text;
    -webkit-user-select: text;
    transition: all 0.1s ease;
  }
  .fluent-input:hover {
    border-color: var(--border-control-hover);
  }
  .fluent-input:focus {
    border-color: var(--accent);
    border-bottom-width: 2px;
  }
  .eye-toggle {
    position: absolute;
    right: 8px;
    background: transparent;
    border: none;
    cursor: pointer;
    color: var(--text-muted);
    padding: 3px 5px;
    display: flex;
    align-items: center;
    border-radius: 3px;
  }
  .eye-toggle:hover {
    color: var(--text-primary);
    background: #f0f0f0;
  }
  select.fluent-input {
    cursor: pointer;
    padding-right: 24px;
  }

  .info-bar {
    background: #fafafa;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 16px;
    height: 32px;
    font-size: 12px;
    color: var(--text-muted);
    border-top: 1px solid #f2f2f2;
  }
  .info-val {
    font-family: "Cascadia Code", "Consolas", monospace;
    color: var(--text-secondary);
    font-weight: 500;
  }


  /* Action Buttons */
  .footer-actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
    margin-top: 14px;
    margin-bottom: 2px;
  }
  .fluent-btn {
    height: 32px;
    padding: 0 18px;
    border-radius: 4px;
    font-size: 13px;
    font-weight: 500;
    cursor: pointer;
    border: 1px solid transparent;
    font-family: inherit;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    transition: all 0.1s ease;
  }
  .fluent-btn-primary {
    background: var(--accent);
    color: var(--accent-text);
  }
  .fluent-btn-primary:hover {
    background: var(--accent-hover);
  }
  .fluent-btn-primary:active {
    background: var(--accent-active);
  }
  .fluent-btn-secondary {
    background: #ffffff;
    color: var(--text-primary);
    border-color: var(--border-control);
    border-bottom-color: #999999;
  }
  .fluent-btn-secondary:hover {
    background: #fbfbfb;
    border-color: var(--border-control-hover);
  }
  .fluent-btn-secondary:active {
    background: #f5f5f5;
  }

  /* Logs Panel */
  .log-view {
    height: 100%;
    display: flex;
    flex-direction: column;
    gap: 10px;
    overflow: hidden;
  }
  .log-top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-shrink: 0;
  }
  .log-subtext {
    font-size: 12.5px;
    font-weight: 500;
    color: var(--text-secondary);
  }
  .log-btns {
    display: flex;
    gap: 8px;
  }
  .mini-btn {
    height: 28px;
    padding: 0 12px;
    font-size: 12px;
    background: #ffffff;
    border: 1px solid var(--border-control);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-family: inherit;
    transition: all 0.1s ease;
  }
  .mini-btn:hover {
    background: #f7f7f7;
    color: var(--text-primary);
    border-color: var(--border-control-hover);
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
    font-family: "Cascadia Code", "Consolas", "Courier New", monospace;
    font-size: 11px;
    line-height: 1.42;
    letter-spacing: -0.2px;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-all;
    user-select: text;
    -webkit-user-select: text;
    box-shadow: inset 0 1px 4px rgba(0, 0, 0, 0.4);
  }
  .console-box::-webkit-scrollbar {
    width: 8px;
  }
  .console-box::-webkit-scrollbar-thumb {
    background: #444444;
    border-radius: 4px;
  }
  .console-box::-webkit-scrollbar-thumb:hover {
    background: #555555;
  }

  /* Toast Notification */
  .toast {
    position: fixed;
    bottom: 20px;
    left: 50%;
    transform: translateX(-50%) translateY(30px);
    background: rgba(32, 32, 32, 0.95);
    color: #ffffff;
    padding: 8px 18px;
    border-radius: 4px;
    font-size: 12.5px;
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
      <button class="nav-item active" data-tab="settings">账号配置</button>
      <button class="nav-item" data-tab="logs">运行日志</button>
    </div>
    <div id="statusBadge" class="status-indicator">
      <span class="status-dot"></span>
      <span id="badgeText">检测中</span>
    </div>
  </div>

  <!-- VIEW 1: Settings -->
  <div id="view-settings" class="view-panel active">
    <div class="settings-container">
      <div class="fluent-card">
        <div class="setting-row">
          <label class="setting-label" for="account">账号</label>
          <div class="control-wrapper">
            <input type="text" id="account" class="fluent-input" placeholder="学号 / 工号" autocomplete="off" spellcheck="false" />
          </div>
        </div>
        <div class="setting-row">
          <label class="setting-label" for="password">密码</label>
          <div class="control-wrapper">
            <input type="password" id="password" class="fluent-input" placeholder="校园网密码" autocomplete="off" />
            <button type="button" id="togglePwd" class="eye-toggle" title="显示密码">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
                <circle cx="12" cy="12" r="3"></circle>
              </svg>
            </button>
          </div>
        </div>
        <div class="setting-row">
          <label class="setting-label" for="operator">运营商</label>
          <div class="control-wrapper">
            <select id="operator" class="fluent-input">
              <option value="移动">中国移动</option>
              <option value="电信">中国电信</option>
              <option value="联通">中国联通</option>
              <option value="校园网">校园网</option>
            </select>
          </div>
        </div>
        <div class="info-bar">
          <span>当前网络链路</span>
          <span id="linkInfo" class="info-val">检测中...</span>
        </div>
      </div>

      <div class="footer-actions">
        <button id="btnReconnect" class="fluent-btn fluent-btn-secondary">重新连接</button>
        <button id="btnSave" class="fluent-btn fluent-btn-primary">保存配置</button>
      </div>
    </div>
  </div>

  <!-- VIEW 2: Logs -->
  <div id="view-logs" class="view-panel">
    <div class="log-view">
      <div class="log-top">
        <span class="log-subtext">实时守护输出控制台</span>
        <div class="log-btns">
          <button id="btnClear" class="mini-btn">清空</button>
          <button id="btnOpenDir" class="mini-btn">数据目录</button>
        </div>
      </div>
      <pre id="logConsole" class="console-box"></pre>
    </div>
  </div>
</div>

<div id="toast" class="toast">
  <span id="toastMsg">提示内容</span>
</div>

<script>
  // Tab switching
  let pendingTab = null;
  function switchTab(name) {
    pendingTab = name;
    const tabs = document.querySelectorAll('.nav-item');
    const panels = document.querySelectorAll('.view-panel');
    if (!tabs.length || !panels.length) return;
    tabs.forEach(t => t.classList.toggle('active', t.getAttribute('data-tab') === name));
    panels.forEach(p => p.classList.toggle('active', p.id === 'view-' + name));
    if (name === 'logs') {
      const el = document.getElementById('logConsole');
      if (el) el.scrollTop = el.scrollHeight;
    }
  }
  window.switchTab = switchTab;

  document.querySelectorAll('.nav-item').forEach(btn => {
    btn.addEventListener('click', () => switchTab(btn.getAttribute('data-tab')));
  });

  // Password visibility toggle
  const pwdInput = document.getElementById('password');
  const togglePwd = document.getElementById('togglePwd');
  togglePwd.addEventListener('click', () => {
    pwdInput.type = pwdInput.type === 'password' ? 'text' : 'password';
  });

  // Toast
  let toastTimer = null;
  function showToast(msg) {
    const toast = document.getElementById('toast');
    document.getElementById('toastMsg').innerText = msg;
    toast.classList.add('show');
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toast.classList.remove('show'), 2200);
  }

  // Dashboard update
  window.onStatusUpdate = function(dash) {
    if (!dash) return;
    const badge = document.getElementById('statusBadge');
    const badgeText = document.getElementById('badgeText');
    const linkInfo = document.getElementById('linkInfo');

    const status = dash.status || '未连接';
    badgeText.innerText = status;

    badge.className = 'status-indicator';
    if (status.includes('正常') || status.includes('已连接') || status.includes('在线')) {
      // online
    } else if (status.includes('重试') || status.includes('中') || status.includes('正在')) {
      badge.classList.add('busy');
    } else {
      badge.classList.add('offline');
    }

    const net = dash.network || '未就绪';
    const ip = dash.ip || '无 IP';
    linkInfo.innerText = net + ' · ' + ip;
  };

  // Logs append
  const logConsole = document.getElementById('logConsole');
  window.onNewLog = function(chunk) {
    logConsole.innerText += chunk;
    if (logConsole.innerText.length > 40000) {
      logConsole.innerText = logConsole.innerText.slice(-30000);
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

  // Save config
  document.getElementById('btnSave').addEventListener('click', async () => {
    const acc = document.getElementById('account').value.trim();
    const pwd = document.getElementById('password').value;
    const op = document.getElementById('operator').value;
    if (!acc || !pwd) {
      showToast('请填写完整的账号和密码');
      return;
    }
    try {
      if (window.saveConfig) {
        await window.saveConfig(acc, pwd, op);
        showToast('配置已保存');
      }
    } catch (e) {
      showToast('保存失败: ' + e);
    }
  });

  // Reconnect
  document.getElementById('btnReconnect').addEventListener('click', async () => {
    try {
      if (window.reconnect) {
        await window.reconnect();
        showToast('已发送重连请求');
      }
    } catch (e) {
      showToast('重连失败: ' + e);
    }
  });

  // Hotkeys
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      if (window.hideWindow) window.hideWindow();
    } else if (e.key === 'Enter' && (e.target.id === 'account' || e.target.id === 'password')) {
      document.getElementById('btnSave').click();
    }
  });

  // Refresh all state
  async function refreshAll() {
    try {
      if (window.getConfig) {
        const cfg = await window.getConfig();
        if (cfg) {
          if (cfg.account) document.getElementById('account').value = cfg.account;
          if (cfg.password) document.getElementById('password').value = cfg.password;
          if (cfg.operator) document.getElementById('operator').value = cfg.operator;
        }
      }
      if (window.getDashboard) {
        const dash = await window.getDashboard();
        if (dash) window.onStatusUpdate(dash);
      }
      if (window.getLogs) {
        const logs = await window.getLogs();
        if (logs) {
          logConsole.innerText = logs;
          logConsole.scrollTop = logConsole.scrollHeight;
        }
      }
    } catch (e) {
      console.error(e);
    }
  }
  window.refreshAll = refreshAll;

  // Init data
  window.addEventListener('DOMContentLoaded', async () => {
    if (pendingTab) switchTab(pendingTab);
    await refreshAll();
    if (window.onUIRendered) {
      window.onUIRendered();
    }
  });
</script>
</body>
</html>
`
