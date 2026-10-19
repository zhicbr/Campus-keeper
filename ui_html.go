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
    --bg-app: #f8fafc;
    --card-bg: #ffffff;
    --text-primary: #0f172a;
    --text-secondary: #475569;
    --text-muted: #94a3b8;
    --border: #e2e8f0;
    --border-hover: #cbd5e1;
    --primary: #2563eb;
    --primary-hover: #1d4ed8;
    --primary-light: #eff6ff;
    --primary-ring: rgba(37, 99, 235, 0.2);
    --success-bg: #ecfdf5;
    --success-border: #a7f3d0;
    --success-text: #065f46;
    --warning-bg: #fffbeb;
    --warning-border: #fde68a;
    --warning-text: #92400e;
    --shadow-sm: 0 1px 2px 0 rgba(0, 0, 0, 0.05);
    --shadow-md: 0 4px 6px -1px rgba(0, 0, 0, 0.05), 0 2px 4px -2px rgba(0, 0, 0, 0.05);
    --radius-md: 10px;
    --radius-lg: 14px;
  }

  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI Variable Text", "Segoe UI", "PingFang SC", "Microsoft YaHei UI", sans-serif;
    background-color: var(--bg-app);
    color: var(--text-primary);
    height: 100vh;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    font-size: 13.5px;
    line-height: 1.5;
  }

  /* Header */
  header {
    background: #ffffff;
    border-bottom: 1px solid var(--border);
    padding: 14px 22px 0 22px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .header-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .brand-logo {
    width: 28px;
    height: 28px;
    border-radius: 8px;
    background: linear-gradient(135deg, #2563eb, #1d4ed8);
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 2px 4px rgba(37, 99, 235, 0.25);
  }
  .brand-logo svg {
    width: 17px;
    height: 17px;
    fill: #ffffff;
  }
  .brand-info h1 {
    font-size: 15px;
    font-weight: 600;
    color: var(--text-primary);
    letter-spacing: -0.2px;
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .brand-info p {
    font-size: 11px;
    color: var(--text-muted);
  }

  /* Tabs Navigation */
  .tabs {
    display: flex;
    gap: 4px;
    position: relative;
  }
  .tab-btn {
    padding: 8px 16px;
    font-size: 13px;
    font-weight: 500;
    color: var(--text-secondary);
    background: none;
    border: none;
    cursor: pointer;
    border-bottom: 2px solid transparent;
    transition: all 0.15s ease;
  }
  .tab-btn:hover {
    color: var(--primary);
  }
  .tab-btn.active {
    color: var(--primary);
    border-bottom-color: var(--primary);
    font-weight: 600;
  }

  /* Main Container */
  main {
    flex: 1;
    overflow-y: auto;
    padding: 18px 22px;
    display: flex;
    flex-direction: column;
  }
  .tab-content {
    display: none;
    flex-direction: column;
    gap: 16px;
    height: 100%;
  }
  .tab-content.active {
    display: flex;
  }

  /* Status Card */
  .card {
    background: var(--card-bg);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    padding: 16px 18px;
    box-shadow: var(--shadow-sm);
  }
  .status-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 14px;
  }
  .card-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-primary);
  }
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    border-radius: 9999px;
    font-size: 12px;
    font-weight: 500;
    background: var(--success-bg);
    color: var(--success-text);
    border: 1px solid var(--success-border);
  }
  .badge-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background-color: currentColor;
  }
  .badge.warning {
    background: var(--warning-bg);
    color: var(--warning-text);
    border-color: var(--warning-border);
  }

  /* Status Grid */
  .status-grid {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 12px;
    background: #f8fafc;
    border-radius: var(--radius-md);
    padding: 12px;
    border: 1px solid #f1f5f9;
  }
  .grid-item {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .grid-label {
    font-size: 11.5px;
    color: var(--text-muted);
  }
  .grid-val {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Form */
  .form-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-bottom: 14px;
  }
  .form-label {
    font-size: 12.5px;
    font-weight: 500;
    color: var(--text-secondary);
  }
  .input-wrapper {
    position: relative;
    display: flex;
    align-items: center;
  }
  .form-control {
    width: 100%;
    height: 38px;
    padding: 0 12px;
    font-size: 13.5px;
    color: var(--text-primary);
    background: #ffffff;
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    transition: all 0.15s ease;
    outline: none;
    font-family: inherit;
    user-select: text;
    -webkit-user-select: text;
  }
  .form-control:hover {
    border-color: var(--border-hover);
  }
  .form-control:focus {
    border-color: var(--border-focus);
    box-shadow: 0 0 0 3px var(--primary-ring);
  }
  select.form-control {
    cursor: pointer;
  }
  .toggle-pwd {
    position: absolute;
    right: 10px;
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .toggle-pwd:hover {
    color: var(--text-primary);
  }

  .dpapi-hint {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    color: var(--text-muted);
    margin-top: 2px;
  }
  .dpapi-hint svg {
    width: 13px;
    height: 13px;
    fill: currentColor;
    flex-shrink: 0;
  }

  /* Action Buttons */
  .actions {
    display: flex;
    gap: 10px;
    margin-top: auto;
    padding-top: 10px;
  }
  .btn {
    height: 38px;
    padding: 0 18px;
    border-radius: var(--radius-md);
    font-size: 13px;
    font-weight: 500;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    transition: all 0.15s ease;
    border: 1px solid transparent;
    font-family: inherit;
  }
  .btn-primary {
    background: var(--primary);
    color: #ffffff;
    flex: 1;
    box-shadow: 0 1px 3px rgba(37, 99, 235, 0.2);
  }
  .btn-primary:hover {
    background: var(--primary-hover);
  }
  .btn-primary:active {
    transform: translateY(1px);
  }
  .btn-secondary {
    background: #ffffff;
    color: var(--text-secondary);
    border-color: var(--border);
  }
  .btn-secondary:hover {
    background: #f8fafc;
    border-color: var(--border-hover);
    color: var(--text-primary);
  }

  /* Log Console */
  #tab-logs {
    height: 100%;
  }
  .log-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }
  .log-options {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .switch-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--text-secondary);
    cursor: pointer;
  }
  .btn-sm {
    height: 28px;
    padding: 0 10px;
    font-size: 12px;
    border-radius: 6px;
    background: #ffffff;
    border: 1px solid var(--border);
    color: var(--text-secondary);
    cursor: pointer;
    transition: all 0.15s ease;
  }
  .btn-sm:hover {
    background: #f1f5f9;
    color: var(--text-primary);
  }

  .console-wrapper {
    flex: 1;
    background: #090d16;
    border: 1px solid #1e293b;
    border-radius: var(--radius-md);
    overflow: hidden;
    display: flex;
    flex-direction: column;
    box-shadow: inset 0 2px 4px rgba(0, 0, 0, 0.3);
  }
  pre#logConsole {
    flex: 1;
    padding: 12px 14px;
    font-family: "Cascadia Code", "Consolas", "Courier New", monospace;
    font-size: 12px;
    line-height: 1.6;
    color: #e2e8f0;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-all;
    user-select: text;
    -webkit-user-select: text;
  }
  pre#logConsole::-webkit-scrollbar {
    width: 8px;
  }
  pre#logConsole::-webkit-scrollbar-track {
    background: #090d16;
  }
  pre#logConsole::-webkit-scrollbar-thumb {
    background: #334155;
    border-radius: 4px;
  }
  pre#logConsole::-webkit-scrollbar-thumb:hover {
    background: #475569;
  }

  /* Toast Notification */
  .toast {
    position: fixed;
    bottom: 24px;
    left: 50%;
    transform: translateX(-50%) translateY(40px);
    background: rgba(15, 23, 42, 0.92);
    color: #ffffff;
    padding: 8px 16px;
    border-radius: 9999px;
    font-size: 12.5px;
    font-weight: 500;
    box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.2);
    opacity: 0;
    transition: all 0.25s cubic-bezier(0.16, 1, 0.3, 1);
    pointer-events: none;
    z-index: 100;
    display: flex;
    align-items: center;
    gap: 8px;
    backdrop-filter: blur(8px);
  }
  .toast.show {
    transform: translateX(-50%) translateY(0);
    opacity: 1;
  }
</style>
</head>
<body>

<header>
  <div class="header-top">
    <div class="brand">
      <div class="brand-logo">
        <svg viewBox="0 0 24 24">
          <path d="M12 1L3 5v6c0 5.55 3.84 10.74 9 12 5.16-1.26 9-6.45 9-12V5l-9-4zm-2 16l-4-4 1.41-1.41L10 14.17l6.59-6.59L18 9l-8 8z"/>
        </svg>
      </div>
      <div class="brand-info">
        <h1>CampusKeeper</h1>
        <p>校园网认证与网络守护</p>
      </div>
    </div>
  </div>

  <div class="tabs">
    <button class="tab-btn active" data-tab="settings">设置</button>
    <button class="tab-btn" data-tab="logs">运行日志</button>
  </div>
</header>

<main>
  <!-- TAB: SETTINGS -->
  <div id="tab-settings" class="tab-content active">
    <!-- Status Card -->
    <div class="card">
      <div class="status-header">
        <span class="card-title">网络连接状态</span>
        <div id="statusBadge" class="badge">
          <span class="badge-dot"></span>
          <span id="badgeText">检测中</span>
        </div>
      </div>
      <div class="status-grid">
        <div class="grid-item">
          <span class="grid-label">链路</span>
          <span id="gridNet" class="grid-val">-</span>
        </div>
        <div class="grid-item">
          <span class="grid-label">运营商</span>
          <span id="gridOp" class="grid-val">-</span>
        </div>
        <div class="grid-item">
          <span class="grid-label">IP 地址</span>
          <span id="gridIP" class="grid-val">-</span>
        </div>
      </div>
    </div>

    <!-- Config Card -->
    <div class="card">
      <div class="form-group">
        <label class="form-label" for="account">账号</label>
        <div class="input-wrapper">
          <input type="text" id="account" class="form-control" placeholder="学号 / 工号" autocomplete="off" spellcheck="false" />
        </div>
      </div>

      <div class="form-group">
        <label class="form-label" for="password">密码</label>
        <div class="input-wrapper">
          <input type="password" id="password" class="form-control" placeholder="校园网登录密码" autocomplete="off" />
          <button type="button" id="togglePwd" class="toggle-pwd" title="显示/隐藏密码">
            <svg id="eyeIcon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path>
              <circle cx="12" cy="12" r="3"></circle>
            </svg>
          </button>
        </div>
      </div>

      <div class="form-group" style="margin-bottom: 8px;">
        <label class="form-label" for="operator">运营商</label>
        <select id="operator" class="form-control">
          <option value="移动">中国移动</option>
          <option value="电信">中国电信</option>
          <option value="联通">中国联通</option>
          <option value="校园网">校园网</option>
        </select>
      </div>

      <div class="dpapi-hint">
        <svg viewBox="0 0 24 24">
          <path d="M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm-6 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zm3.1-9H8.9V6c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2z"/>
        </svg>
        <span>账号密码经 Windows DPAPI 硬件级本地加密保护</span>
      </div>
    </div>

    <!-- Actions -->
    <div class="actions">
      <button id="btnSave" class="btn btn-primary">保存配置</button>
      <button id="btnReconnect" class="btn btn-secondary">立即重连</button>
    </div>
  </div>

  <!-- TAB: LOGS -->
  <div id="tab-logs" class="tab-content">
    <div class="log-toolbar">
      <span class="card-title">系统日志</span>
      <div class="log-options">
        <label class="switch-label">
          <input type="checkbox" id="autoScroll" checked /> 自动滚动
        </label>
        <button id="btnClear" class="btn-sm">清空</button>
        <button id="btnOpenDir" class="btn-sm">数据目录</button>
      </div>
    </div>
    <div class="console-wrapper">
      <pre id="logConsole"></pre>
    </div>
  </div>
</main>

<div id="toast" class="toast">
  <span id="toastMsg">提示内容</span>
</div>

<script>
  // Tabs switching
  const tabs = document.querySelectorAll('.tab-btn');
  const tabContents = document.querySelectorAll('.tab-content');

  function switchTab(name) {
    tabs.forEach(t => {
      t.classList.toggle('active', t.getAttribute('data-tab') === name);
    });
    tabContents.forEach(c => {
      c.classList.toggle('active', c.id === 'tab-' + name);
    });
    if (name === 'logs') {
      scrollLogs();
    }
  }

  tabs.forEach(btn => {
    btn.addEventListener('click', () => {
      switchTab(btn.getAttribute('data-tab'));
    });
  });

  // Password visibility toggle
  const pwdInput = document.getElementById('password');
  const togglePwd = document.getElementById('togglePwd');
  togglePwd.addEventListener('click', () => {
    if (pwdInput.type === 'password') {
      pwdInput.type = 'text';
    } else {
      pwdInput.type = 'password';
    }
  });

  // Toast
  let toastTimer = null;
  function showToast(msg) {
    const toast = document.getElementById('toast');
    document.getElementById('toastMsg').innerText = msg;
    toast.classList.add('show');
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      toast.classList.remove('show');
    }, 2400);
  }

  // Dashboard status renderer
  window.onStatusUpdate = function(dash) {
    const badge = document.getElementById('statusBadge');
    const badgeText = document.getElementById('badgeText');
    badgeText.innerText = dash.status || '未连接';

    if (dash.status && (dash.status.includes('正常') || dash.status.includes('已连接') || dash.status.includes('在线'))) {
      badge.className = 'badge';
    } else {
      badge.className = 'badge warning';
    }

    document.getElementById('gridNet').innerText = dash.network || '-';
    document.getElementById('gridOp').innerText = dash.operator || '-';
    document.getElementById('gridIP').innerText = dash.ip || '-';
  };

  // Logs append & stream
  const logConsole = document.getElementById('logConsole');
  const autoScrollCheck = document.getElementById('autoScroll');

  function scrollLogs() {
    if (autoScrollCheck.checked) {
      logConsole.scrollTop = logConsole.scrollHeight;
    }
  }

  window.onNewLog = function(chunk) {
    logConsole.innerText += chunk;
    if (logConsole.innerText.length > 50000) {
      logConsole.innerText = logConsole.innerText.slice(-40000);
    }
    scrollLogs();
  };

  document.getElementById('btnClear').addEventListener('click', () => {
    logConsole.innerText = '';
    showToast('日志显示已清空');
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
        showToast('配置已加密保存，正在验证连接');
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
      showToast('重连请求失败: ' + e);
    }
  });

  // Hotkeys: ESC to hide
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      if (window.hideWindow) window.hideWindow();
    } else if (e.key === 'Enter' && (e.target.id === 'account' || e.target.id === 'password')) {
      document.getElementById('btnSave').click();
    }
  });

  // Initialize data on load
  window.addEventListener('DOMContentLoaded', async () => {
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
          scrollLogs();
        }
      }
    } catch (e) {
      console.error(e);
    }
  });
</script>
</body>
</html>
`
