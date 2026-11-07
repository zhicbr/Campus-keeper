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
  .fluent-input::placeholder {
    color: var(--text-muted);
    font-size: 12px;
  }
  .fluent-input:disabled {
    background: #f7f7f7;
    color: var(--text-muted);
    cursor: not-allowed;
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
    align-items: center;
    margin-top: 14px;
    margin-bottom: 2px;
  }
  .action-buttons {
    display: flex;
    gap: 10px;
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
  .fluent-btn-primary:not(:disabled):active {
    background: var(--accent-active);
  }
  .fluent-btn-secondary {
    background: #ffffff;
    color: var(--text-primary);
    border-color: var(--border-control);
    border-bottom-color: #999999;
  }
  .fluent-btn-secondary:not(:disabled):hover {
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
    justify-content: flex-end;
    align-items: center;
    flex-shrink: 0;
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
        <div class="info-bar">
          <span>当前网络链路</span>
          <span id="linkInfo" class="info-val">检测中...</span>
        </div>
      </div>

      <div class="footer-actions">
        <div class="action-buttons" style="display: flex; gap: 8px; width: 100%;">
          <button id="btnExit" class="fluent-btn fluent-btn-secondary" style="margin-right: auto; color: #d13438;">退出程序</button>
          <button id="btnReconnect" class="fluent-btn fluent-btn-secondary">重新连接</button>
          <button id="btnSave" class="fluent-btn fluent-btn-primary">保存配置</button>
        </div>
      </div>
    </div>
  </div>

  <!-- VIEW 2: Logs -->
  <div id="view-logs" class="view-panel">
    <div class="log-view">
      <div class="log-top">
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

  let savedAccount = '';
  let hasSavedPassword = false;

  function updatePasswordPlaceholder() {
    const curAcc = document.getElementById('account').value.trim();
    const pwdEl = document.getElementById('password');
    const isDifferent = Boolean(savedAccount && curAcc.toLowerCase() !== savedAccount.toLowerCase());
    if (!hasSavedPassword || isDifferent) {
      pwdEl.placeholder = (savedAccount && isDifferent)
        ? '请输入新密码'
        : '校园网密码';
    } else {
      pwdEl.placeholder = '●●●●●●●● (留空保持原密码)';
    }
  }

  document.getElementById('account').addEventListener('input', updatePasswordPlaceholder);

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

  function setSavingState(saving) {
    document.getElementById('account').disabled = saving;
    document.getElementById('password').disabled = saving;
    document.getElementById('operator').disabled = saving;
    const btn = document.getElementById('btnSave');
    btn.disabled = saving;
    btn.innerText = saving ? '保存中...' : '保存配置';
  }

  // Save config
  document.getElementById('btnSave').addEventListener('click', async () => {
    const accInput = document.getElementById('account');
    const pwdInput = document.getElementById('password');
    const opInput = document.getElementById('operator');

    const acc = accInput.value.trim();
    const pwd = pwdInput.value;
    const op = opInput.value;

    if (!acc) {
      showToast('请填写账号');
      return;
    }

    setSavingState(true);
    try {
      if (window.saveConfig) {
        const changed = await window.saveConfig(acc, pwd, op);
        if (!changed) {
          showToast('配置未做更改');
          return;
        }
        savedAccount = acc;
        hasSavedPassword = true;
        pwdInput.value = '';
        updatePasswordPlaceholder();
        showToast('配置已保存');
      }
    } catch (e) {
      const msg = (e && e.message) ? e.message : String(e);
      if (msg.includes('不能为空') || msg.includes('必须输入') || msg.includes('未知运营商')) {
        showToast(msg);
      } else {
        showToast('保存失败: ' + msg);
      }
    } finally {
      setSavingState(false);
    }
  });

  // Reconnect
  document.getElementById('btnExit').addEventListener('click', async () => {
    if (confirm('确定要彻底退出校园网守护程序吗？退出后将停止自动保活。')) {
      if (window.exitApp) window.exitApp();
    }
  });

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
          savedAccount = (cfg.account || '').trim();
          hasSavedPassword = !!cfg.hasPassword;
          document.getElementById('account').value = savedAccount;
          if (cfg.operator) document.getElementById('operator').value = cfg.operator;
          document.getElementById('password').value = '';
          updatePasswordPlaceholder();
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
