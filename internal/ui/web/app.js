/* Pad2Go web interface. State comes from the local pad2go process:
   GET /api/state, live updates over /api/events, input tests over
   /api/players/{n}/input. Settings are saved to config.yaml by the server. */
(() => {
  'use strict';
  const root = document.getElementById('pad2go');
  const el = id => document.getElementById(id);
  const all = query => [...root.querySelectorAll(query)];
  const prefsKey = 'pad2go-ui-v1';
  const ui = { view: 'controllers', theme: 'light', detail: null, testing: null, discoveryOpen: false, notifications: true, desktopNotifications: false };
  let snap = null;
  let draft = null;
  let inputStream = null;
  let saveTimer = null;
  let saving = false; // a save is scheduled or in flight: keep the local draft
  try {
    const saved = JSON.parse(localStorage.getItem(prefsKey));
    if (saved) Object.assign(ui, { theme: saved.theme || 'light', notifications: saved.notifications !== false, desktopNotifications: Boolean(saved.desktopNotifications) });
  } catch { /* Storage is optional. */ }
  if (!localStorage.getItem(prefsKey) && matchMedia('(prefers-color-scheme: dark)').matches) ui.theme = 'dark';

  const paths = {
    gamepad: '<path d="M6 8h12c2 0 3 2 3 4l1 5c.5 3-2 4-4 2l-2-2H8l-2 2c-2 2-4.5 1-4-2l1-5c0-2 1-4 3-4Z"/><path d="M6 11v5m-2.5-2.5h5m7.5-1h.01m2 2h.01"/>',
    orbit: '<circle cx="12" cy="12" r="3"/><ellipse cx="12" cy="12" rx="10" ry="5" transform="rotate(-35 12 12)"/>',
    sliders: '<path d="M3 6h4m4 0h10M3 12h10m4 0h4M3 18h2m4 0h12"/><circle cx="9" cy="6" r="2"/><circle cx="15" cy="12" r="2"/><circle cx="7" cy="18" r="2"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    bluetooth: '<path d="m7 7 10 10-5 4V3l5 4L7 17"/>',
    monitor: '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M12 17v4m-4 0h8"/>',
    'arrow-right': '<path d="M4 12h16m-6-6 6 6-6 6"/>',
    check: '<path d="m5 12 4 4L19 6"/>',
    x: '<path d="m6 6 12 12M6 18 18 6"/>',
    moon: '<path d="M21 12.8A9 9 0 0 1 11.2 3 9 9 0 1 0 21 12.8Z"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1 1m12 12 1 1M5 19l1-1M18 6l1-1"/>',
    copy: '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V4H4v12h4"/>',
    radio: '<circle cx="12" cy="12" r="2"/><path d="M7 7a7 7 0 0 0 0 10m10-10a7 7 0 0 1 0 10M4 4a11 11 0 0 0 0 16M20 4a11 11 0 0 1 0 16"/>',
    battery: '<rect x="2" y="6" width="17" height="12" rx="2"/><path d="M22 10v4M5 9v6m3-6v6m3-6v6"/>',
    'battery-low': '<rect x="2" y="6" width="17" height="12" rx="2"/><path d="M22 10v4M5 9v6"/>',
    vibrate: '<rect x="8" y="4" width="8" height="16" rx="2"/><path d="m4 5-2 3 2 4-2 4 2 3m16-14 2 3-2 4 2 4-2 3"/>',
  };
  const icon = name => `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || paths.check}</svg>`;
  const escape = text => String(text).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
  function icons() { all('[data-icon]').forEach(node => { node.outerHTML = icon(node.dataset.icon); }); }
  function persistPrefs() {
    try { localStorage.setItem(prefsKey, JSON.stringify({ theme: ui.theme, notifications: ui.notifications, desktopNotifications: ui.desktopNotifications })); } catch { /* Optional. */ }
  }
  function announce(message, error = false) { el('feedback').textContent = message; el('feedback').classList.toggle('error', error); el('feedback').setAttribute('role', error ? 'alert' : 'status'); }

  async function api(path, { method = 'GET', body } = {}) {
    const response = await fetch(path, { method, headers: { 'X-Pad2go': '1', ...(body ? { 'Content-Type': 'application/json' } : {}) }, body: body ? JSON.stringify(body) : undefined });
    if (!response.ok) {
      let message = response.statusText;
      try { message = (await response.json()).error || message; } catch { /* Not JSON. */ }
      throw new Error(message);
    }
    return response.status === 200 ? response.json() : null;
  }

  function notifyConnection({ name, connected, player }) {
    if (!ui.notifications) return;
    const container = el('connection-toasts');
    while (container.children.length >= 2) container.firstElementChild.remove();
    const toast = document.createElement('section');
    toast.className = `connection-toast ${connected ? 'connected' : 'disconnected'}`;
    const top = document.createElement('div'); top.className = 'toast-top';
    const image = document.createElement('img'); image.src = 'assets/pad2go-icon.png'; image.alt = '';
    const status = document.createElement('span'); status.className = 'toast-state'; status.textContent = connected ? 'Controle conectado' : 'Controle desconectado';
    const close = document.createElement('button'); close.type = 'button'; close.className = 'toast-close'; close.setAttribute('aria-label', 'Fechar aviso de conexão'); close.innerHTML = icon('x'); close.addEventListener('click', () => toast.remove());
    top.append(image, status, close);
    const title = document.createElement('h3'); title.textContent = name;
    const body = document.createElement('p'); body.textContent = `Jogador ${player} · ${connected ? 'Pronto para usar.' : 'Conexão encerrada.'}`;
    toast.append(top, title, body); container.append(toast);
    setTimeout(() => toast.remove(), 5500);
    if (ui.desktopNotifications && 'Notification' in window && Notification.permission === 'granted') {
      try { new Notification(`Pad2Go · ${name} ${connected ? 'conectado' : 'desconectado'}`, { body: body.textContent, icon: new URL('assets/pad2go-icon.png', location.href).href, tag: `pad2go-player-${player}` }); } catch { /* The window toast remains. */ }
    }
  }

  function joy(right = false) { return `<div class="joy ${right ? 'right' : ''}"><span class="stick"></span>${[1, 2, 3, 4].map(i => `<span class="hw-key k${i}"></span>`).join('')}</div>`; }
  function art(members) {
    if (members.length === 2) return joy() + joy(true);
    const kind = members[0].kind;
    if (kind === 'left' || kind === 'right') return joy(kind === 'right');
    return `<div class="pro ${kind === 'gc' ? 'gamecube' : ''}"><span class="stick"></span><span class="stick second"></span><span class="cross">+</span>${[1, 2, 3, 4].map(i => `<span class="hw-key k${i}"></span>`).join('')}</div>`;
  }
  const ledPatterns = [0, 1, 3, 7, 15, 9, 5, 13, 6]; // protocol.LEDPattern
  function leds(player) {
    return `<span class="leds" role="img" aria-label="LEDs do jogador ${player}">${[0, 1, 2, 3].map(bit => `<span class="${ledPatterns[player] & (1 << bit) ? 'lit' : ''}"></span>`).join('')}</span>`;
  }
  const lowBattery = member => member.battery >= 0 && member.battery <= 15;

  let devicesKey = '';
  function renderDevices() {
    const players = snap.players;
    // Rebuild the cards only when what they show changed, so periodic state
    // refreshes don't steal focus or close an open select.
    el('player-count').textContent = `${players.length} ${players.length === 1 ? 'jogador' : 'jogadores'}`;
    el('controllers-subtitle').textContent = players.length ? snap.output.motionOnly ? 'Prontos para enviar movimento ao emulador.' : 'Prontos para o próximo jogo.' : 'Aguardando um controle em SYNC ou já pareado.';
    if (ui.testing !== null && !players.some(p => p.player === ui.testing)) { stopTest(); el('test-panel').hidden = true; }
    if (ui.detail !== null && !players.some(p => p.player === ui.detail)) ui.detail = null;
    const key = JSON.stringify([players, ui.detail, ui.testing, snap.config.layout, draft.hold, draft.holds, snap.output.motionOnly]);
    if (key === devicesKey) return;
    devicesKey = key;
    el('device-list').innerHTML = players.length ? players.map(slot => {
      const pair = slot.members.length === 2;
      const low = slot.members.some(lowBattery);
      const single = slot.members.length === 1 && ['left', 'right'].includes(slot.members[0].kind);
      const batteries = slot.members.filter(m => m.battery >= 0).map(m => `<span class="battery ${lowBattery(m) ? 'low' : ''}">${icon(lowBattery(m) ? 'battery-low' : 'battery')}${pair ? (m.kind === 'left' ? 'L ' : 'R ') : ''}~${m.battery}%</span>`).join('');
      return `<article class="device" data-player="${slot.player}" data-testing="${ui.testing === slot.player}">
        <div class="device-heading"><span class="player">Jogador ${slot.player}</span>${leds(slot.player)}</div>
        <div class="device-art" role="img" aria-label="Ilustração de ${escape(slot.name)}">${art(slot.members)}</div>
        <div class="device-info"><h3>${escape(slot.name)}</h3><span class="status ${low ? 'warning' : ''}">${low ? 'Bateria baixa' : 'Conectado'}</span>
          <div class="batteries">${batteries}</div>
          <div class="device-actions"><button type="button" class="button" data-test="${slot.player}">${icon('gamepad')}Testar</button><button type="button" class="text-button" data-detail="${slot.player}" aria-expanded="${ui.detail === slot.player}">Detalhes</button></div>
        </div>
        ${ui.detail === slot.player ? `<div class="device-details"><p>Bluetooth LE · layout ${escape(snap.config.layout)}${slot.members.some(m => m.battery >= 0) ? ' · bateria estimada pela tensão' : ''} · ${slot.members.map(m => escape(m.addr)).join(', ')}</p>${single ? `<label>Posição deste Joy-Con<select data-device-hold="${escape(slot.members[0].addr)}"><option value="Vertical">Vertical</option><option value="Horizontal">Horizontal</option></select></label><p>Salvo automaticamente. Reiniciar para aplicar.</p>` : ''}<button type="button" class="text-button disconnect" data-disconnect="${slot.player}">Desconectar ${pair ? 'par' : 'controle'}</button></div>` : ''}
      </article>`;
    }).join('') : '<div class="empty"><h3>Vamos conectar seu controle?</h3><p>Segure SYNC em um controle novo. Se já estiver pareado, pressione um botão.</p></div>';
    all('[data-device-hold]').forEach(node => { node.value = draft.holds?.[node.dataset.deviceHold] || draft.hold; });
  }

  function renderDiscovery() {
    const d = snap.discovery;
    el('discovery').hidden = !ui.discoveryOpen;
    el('discovery-steps').innerHTML = ['Procurando', 'Encontrado', 'Conectando', 'Pareando', 'Pronto'].map((name, index) => {
      const current = index === d.step && !d.failure && !d.foreign;
      const cls = d.failure && index === d.step ? 'current' : current ? 'current' : index < d.step ? 'done' : '';
      return `<li class="${cls}" ${current ? 'aria-current="step"' : ''}>${name}</li>`;
    }).join('');
    el('discovery-status').textContent = snap.fault ? 'Busca pausada: resolva o problema indicado acima.' : d.text || 'A busca é automática. Não é necessário escolher um controle.';
    el('foreign-action').hidden = !d.foreign || snap.config.foreign;
  }

  function renderSettings() {
    all('[data-config]').forEach(node => {
      const value = draft[node.dataset.config];
      if (node.type === 'checkbox') node.checked = value;
      else if (document.activeElement !== node) node.value = value;
    });
    all('[data-deadzone]').forEach(node => { if (document.activeElement !== node) node.value = draft.deadzones[node.dataset.deadzone]; });
    all('[data-remap]').forEach(node => { node.value = draft.remaps[node.dataset.remap] || 'Default'; });
    all('[data-layout]').forEach(button => button.setAttribute('aria-pressed', button.dataset.layout === draft.layout));
    el('vibration-value').value = `${draft.vibration * 20}%`;
    el('sensitivity-value').value = `Nível ${draft.sensitivity} · ${(1 + (draft.sensitivity - 1) / 12).toFixed(2).replace('.', ',')}×`;
  }

  function render() {
    document.documentElement.dataset.theme = ui.theme;
    el('theme').innerHTML = icon(ui.theme === 'light' ? 'moon' : 'sun');
    el('theme').setAttribute('aria-label', `Ativar tema ${ui.theme === 'light' ? 'escuro' : 'claro'}`);
    el('connection-notifications').checked = ui.notifications;
    el('desktop-notifications').textContent = ui.desktopNotifications ? 'Desativar notificações no desktop' : 'Ativar notificações no desktop';
    all('[data-view]').forEach(button => { const selected = button.dataset.view === ui.view; button.setAttribute('aria-selected', selected); button.tabIndex = selected ? 0 : -1; el(`panel-${button.dataset.view}`).hidden = !selected; });
    if (!snap) return;
    const mac = snap.platform === 'macOS';
    el('platform').textContent = snap.platform;
    el('version').textContent = `pad2go ${snap.version} · ${location.host}`;
    renderSettings();
    el('pending').hidden = !snap.pending && !snap.restarting;
    el('apply-config').disabled = snap.restarting;
    el('apply-config').textContent = snap.restarting ? 'Reiniciando…' : 'Reiniciar conexões';
    el('platform-notice').hidden = !mac;
    [...el('backend').options].forEach(option => { option.disabled = option.value === 'vigem' && snap.platform !== 'Windows' || option.value === 'uinput' && snap.platform !== 'Linux' || option.value === 'auto' && mac; });
    el('host-mac').placeholder = mac ? 'Informar para parear' : 'Detectar automaticamente';
    el('output-name').textContent = snap.output.name;
    el('output-detail').textContent = snap.output.detail;
    el('bluetooth-state').textContent = { on: 'Bluetooth ativo', off: 'Bluetooth indisponível', idle: 'Bluetooth não iniciado' }[snap.bluetooth] || 'Bluetooth';
    const dsu = snap.dsu;
    el('motion-state').textContent = dsu.running ? 'Servidor ativo' : dsu.error ? 'Falha ao iniciar' : dsu.enabled ? 'Aguardando reinício' : 'Desativado';
    el('motion-state').classList.toggle('error', Boolean(dsu.error));
    el('motion-state').classList.toggle('warning', !dsu.running && !dsu.error && dsu.enabled);
    el('motion-address').textContent = dsu.error ? `${dsu.address} · ${dsu.error}` : dsu.address;
    el('client-count').textContent = `${dsu.clients} ${dsu.clients === 1 ? 'cliente DSU recebendo' : 'clientes DSU recebendo'}`;
    const fault = snap.fault;
    el('system-alert').hidden = !fault;
    if (fault) {
      const title = fault.kind === 'bluetooth' ? 'O Bluetooth não está disponível.' : fault.kind === 'output' ? 'Não foi possível iniciar a saída de controle virtual.' : 'Problema na configuração.';
      el('system-alert').innerHTML = `<strong>${title}</strong><p>${escape(fault.message)}</p><div class="alert-actions"><button type="button" class="button" data-retry>${snap.restarting ? 'Tentando…' : 'Tentar novamente'}</button><button type="button" class="text-button" data-help="${fault.kind === 'bluetooth' ? 'bluetooth' : 'output'}">Como resolver</button></div>`;
    }
    el('connect').disabled = Boolean(fault);
    el('connection-summary').textContent = fault ? 'Ação necessária' : snap.restarting ? 'Reiniciando conexões…' : snap.discovery.step >= 1 && snap.discovery.step < 4 && !snap.discovery.failure ? 'Conectando…' : 'Busca automática ativa';
    el('test-panel').hidden = ui.testing === null || ui.view !== 'controllers';
    if (ui.testing !== null) el('test-title').textContent = `Teste de entrada · Jogador ${ui.testing}`;
    renderDevices(); renderDiscovery(); icons();
  }

  // --- Input test ---------------------------------------------------------
  const buttonIds = { up: 0x0001, down: 0x0002, left: 0x0004, right: 0x0008, l: 0x0100, r: 0x0200, a: 0x1000, b: 0x2000, x: 0x4000, y: 0x8000 };
  const number = (value, digits = 2) => value.toFixed(digits).replace('.', ',');
  function showInput(sample) {
    Object.entries(buttonIds).forEach(([id, bit]) => el(`pad-${id}`).classList.toggle('active', Boolean(sample.buttons & bit)));
    el('stick-left').style.transform = `translate(${sample.lx * 14}px,${-sample.ly * 14}px)`;
    el('stick-right').style.transform = `translate(${sample.rx * 14}px,${-sample.ry * 14}px)`;
    el('left-values').value = `X ${number(sample.lx)} · Y ${number(sample.ly)}`;
    el('right-values').value = `X ${number(sample.rx)} · Y ${number(sample.ry)}`;
    [['l', sample.lt], ['r', sample.rt]].forEach(([side, raw]) => {
      const value = Math.round(raw / 255 * 100);
      el(`trigger-${side}`).value = value; el(`trigger-${side}-value`).value = `${value}%${sample.analog ? '' : ' · digital'}`;
    });
    el('gyro-values').value = `X ${number(sample.gyro[0], 1)} · Y ${number(sample.gyro[1], 1)} · Z ${number(sample.gyro[2], 1)}`;
    el('input-state').textContent = 'Recebendo entradas.';
  }
  function resetInput() { showInput({ buttons: 0, lx: 0, ly: 0, rx: 0, ry: 0, lt: 0, rt: 0, analog: false, gyro: [0, 0, 0] }); el('input-state').textContent = 'Aguardando entradas.'; }
  function stopTest() { inputStream?.close(); inputStream = null; ui.testing = null; }
  function startTest(player) {
    stopTest(); ui.testing = player; resetInput();
    inputStream = new EventSource(`/api/players/${player}/input`);
    inputStream.addEventListener('input', event => showInput(JSON.parse(event.data)));
    inputStream.onerror = () => { if (ui.testing === player) el('input-state').textContent = 'Sem entradas: o controle desconectou?'; };
  }

  // --- Settings -----------------------------------------------------------
  function validate(node) {
    const key = node.dataset.config;
    let message = '';
    if (key === 'host' && !node.value.trim()) message = 'Informe o endereço do servidor.';
    if (key === 'port' && (!node.value || !Number.isInteger(Number(node.value)) || Number(node.value) < 1 || Number(node.value) > 65535)) message = 'Use uma porta entre 1 e 65535.';
    if (key === 'hostMac' && node.value.trim() && !/^(?:[a-f\d]{12}|(?:[a-f\d]{2}:){5}[a-f\d]{2}|(?:[a-f\d]{2}-){5}[a-f\d]{2})$/i.test(node.value.trim())) message = 'Use AA:BB:CC:DD:EE:FF.';
    if (node.hasAttribute('data-deadzone') && (!node.value || !Number.isFinite(Number(node.value)) || Number(node.value) < 0 || Number(node.value) > 50)) message = 'Use uma zona morta entre 0% e 50%.';
    node.setAttribute('aria-invalid', Boolean(message));
    const error = el(`${key}-error`);
    if (error) error.textContent = message;
    if (message) announce(`${message} A última configuração válida foi mantida.`, true);
    return !message;
  }
  function save(delay = 0) {
    clearTimeout(saveTimer);
    saving = true;
    saveTimer = setTimeout(async () => {
      try {
        snap = await api('/api/config', { method: 'PUT', body: draft });
        draft = structuredClone(snap.config);
        announce('Salvo no config.yaml. Reinicie as conexões para aplicar.');
      } catch (error) {
        announce(`Não foi possível salvar: ${error.message}`, true);
        draft = structuredClone(snap.config);
      } finally {
        saving = false;
      }
      render();
    }, delay);
  }
  async function restart() {
    try { await api('/api/restart', { method: 'POST' }); ui.testing = null; stopTest(); announce('Conexões reiniciadas. Pressione um botão nos controles para reconectar.'); } catch (error) { announce(`Não foi possível reiniciar: ${error.message}`, true); }
  }

  // --- Diagnostics ----------------------------------------------------------
  const help = {
    bluetooth: () => snap.platform === 'macOS' ? 'Na primeira execução o macOS pergunta se o Pad2Go pode usar o Bluetooth: clique em Permitir. Se você recusou, ative o Pad2Go em Ajustes do Sistema → Privacidade e Segurança → Bluetooth. Confira também se o Bluetooth está ligado e clique em “Tentar novamente”.' : 'Ative o Bluetooth nas configurações do computador e confirme que há um adaptador compatível com Bluetooth LE. Depois, clique em “Tentar novamente”.',
    output: () => snap.platform === 'Windows' ? 'Instale o <a href="https://github.com/nefarius/ViGEmBus" target="_blank" rel="noreferrer">ViGEmBus</a> e reinicie o Pad2Go. Se o jogo detectar entradas duplicadas, configure o <a href="https://github.com/nefarius/HidHide" target="_blank" rel="noreferrer">HidHide</a> para ocultar o controle físico.' : 'Carregue o módulo com <code>sudo modprobe uinput</code>. Verifique se /dev/uinput existe e conceda acesso ao seu usuário com uma regra udev.',
    mac: 'Informe o endereço Bluetooth do adaptador do computador em Ajustes → Conexão → Endereço Bluetooth do PC. Sem ele, a conexão funciona, mas o pareamento para reconectar com um toque não é salvo.',
  };
  function showHelp(button) {
    const old = button.parentElement.querySelector('.diagnostic-help');
    if (old) { old.remove(); return; }
    const paragraph = document.createElement('p'); paragraph.className = 'diagnostic-help';
    const content = help[button.dataset.help]; paragraph.innerHTML = typeof content === 'function' ? content() : content;
    button.parentElement.append(paragraph);
  }
  async function diagnose() {
    try {
      const rows = await api('/api/diagnostics');
      el('diagnostic-results').innerHTML = rows.map(row => `<div class="diagnostic-row"><div><span class="${row.ok ? 'ok' : 'missing'}">${icon(row.ok ? 'check' : 'x')}</span><strong>${escape(row.name)}</strong><span>${escape(row.detail)}</span></div>${!row.ok && row.help ? `<button type="button" class="text-button" data-help="${escape(row.help)}">Como resolver</button>` : ''}</div>`).join('');
    } catch (error) { announce(`Não foi possível verificar: ${error.message}`, true); }
  }

  // --- Events ---------------------------------------------------------------
  function setView(view, focus = false) { if (view !== ui.view && ui.view === 'controllers') stopTest(); ui.view = view; announce(''); render(); if (view === 'settings') diagnose(); if (focus) el(`tab-${view}`).focus(); }
  const remapLabels = { Default: 'Padrão', None: 'Sem ação', MINUS: '−', PLUS: '+', L_STK: 'Clique analógico L', R_STK: 'Clique analógico R', UP: 'Cima', DOWN: 'Baixo', LEFT: 'Esquerda', RIGHT: 'Direita', HOME: 'Home', CAPT: 'Captura' };
  const remapValues = ['Default', 'None', 'A', 'B', 'X', 'Y', 'L', 'R', 'ZL', 'ZR', 'MINUS', 'PLUS', 'L_STK', 'R_STK', 'UP', 'DOWN', 'LEFT', 'RIGHT', 'HOME', 'CAPT'];
  el('remaps').innerHTML = [['home', 'Home'], ['capt', 'Captura'], ['c', 'C'], ['gl', 'GL'], ['gr', 'GR'], ['sll', 'SL (L)'], ['srl', 'SR (L)'], ['slr', 'SL (R)'], ['srr', 'SR (R)']].map(([key, name]) => `<label>${name}<select data-remap="${key}">${remapValues.map(value => `<option value="${value}">${remapLabels[value] || value}</option>`).join('')}</select></label>`).join('');

  root.addEventListener('click', async event => {
    const button = event.target.closest('button'); if (!button || button.disabled) return;
    if (button.dataset.view) setView(button.dataset.view);
    else if (button.id === 'theme') { ui.theme = ui.theme === 'light' ? 'dark' : 'light'; persistPrefs(); render(); }
    else if (button.id === 'connect') { ui.discoveryOpen = true; render(); el('discovery').scrollIntoView({ block: 'nearest' }); }
    else if (button.id === 'hide-discovery') { ui.discoveryOpen = false; render(); el('connect').focus(); }
    else if (button.id === 'accept-foreign') { draft.foreign = true; clearTimeout(saveTimer); try { snap = await api('/api/config', { method: 'PUT', body: draft }); await restart(); } catch (error) { announce(`Não foi possível salvar: ${error.message}`, true); } }
    else if (button.id === 'apply-config' || button.hasAttribute('data-retry')) await restart();
    else if (button.dataset.detail) { const player = Number(button.dataset.detail); ui.detail = ui.detail === player ? null : player; render(); root.querySelector(`[data-detail="${player}"]`)?.focus(); }
    else if (button.dataset.test) { startTest(Number(button.dataset.test)); render(); el('test-panel').scrollIntoView({ block: 'nearest' }); el('rumble').focus({ preventScroll: true }); }
    else if (button.id === 'close-test') { const player = ui.testing; stopTest(); render(); root.querySelector(`[data-test="${player}"]`)?.focus(); }
    else if (button.dataset.disconnect) {
      const player = Number(button.dataset.disconnect);
      try { await api(`/api/players/${player}/disconnect`, { method: 'POST' }); ui.detail = null; announce(`Jogador ${player} desconectado. Os demais jogadores mantêm seus números.`); el('connect').focus(); } catch (error) { announce(`Não foi possível desconectar: ${error.message}`, true); }
    }
    else if (button.dataset.layout) { draft.layout = button.dataset.layout; render(); save(); root.querySelector(`[data-layout="${draft.layout}"]`)?.focus(); }
    else if (button.id === 'rumble') {
      try { await api(`/api/players/${ui.testing}/rumble`, { method: 'POST' }); el('input-state').textContent = `Vibração enviada · intensidade ${snap.config.vibration * 20}% (aplicada após reinício, se alterada).`; } catch (error) { el('input-state').textContent = `Não foi possível vibrar: ${error.message}`; }
    }
    else if (button.id === 'copy-address') { const address = snap.dsu.address; try { await navigator.clipboard.writeText(address); el('copy-result').textContent = 'Endereço copiado.'; } catch { el('copy-result').textContent = `Não foi possível copiar automaticamente. Copie: ${address}`; } }
    else if (button.id === 'desktop-notifications') {
      if (ui.desktopNotifications) { ui.desktopNotifications = false; persistPrefs(); render(); el('notification-permission').textContent = 'Notificações nativas desativadas. Os avisos da janela continuam disponíveis.'; return; }
      if (!('Notification' in window)) { el('notification-permission').textContent = 'Este navegador não oferece notificações nativas. Os avisos da janela continuam disponíveis.'; return; }
      try { const permission = await Notification.requestPermission(); ui.desktopNotifications = permission === 'granted'; persistPrefs(); render(); el('notification-permission').textContent = permission === 'granted' ? 'Notificações do desktop ativadas. O formato do aviso é definido pelo sistema operacional.' : 'Permissão não concedida. Você pode habilitá-la nas configurações do navegador.'; } catch { el('notification-permission').textContent = 'Não foi possível solicitar a permissão. Os avisos da janela continuam disponíveis.'; }
    }
    else if (button.dataset.help) showHelp(button);
    else if (button.id === 'check-system') { await diagnose(); announce('Verificação concluída.'); }
  });
  root.addEventListener('change', event => {
    const node = event.target;
    if (node.id === 'connection-notifications') { ui.notifications = node.checked; persistPrefs(); announce('Preferência de avisos salva neste navegador.'); if (!node.checked) el('connection-toasts').replaceChildren(); return; }
    if (!draft) return;
    if (node.dataset.config && node.type !== 'range') {
      if (!validate(node)) return;
      const key = node.dataset.config;
      draft[key] = node.type === 'checkbox' ? node.checked : ['port', 'max'].includes(key) ? Number(node.value) : node.value.trim();
      save();
    }
    if (node.hasAttribute('data-deadzone')) { if (!validate(node)) return; draft.deadzones[node.dataset.deadzone] = Number(node.value); save(); }
    if (node.hasAttribute('data-remap')) { draft.remaps[node.dataset.remap] = node.value; save(); }
    if (node.dataset.deviceHold) { draft.holds = { ...draft.holds, [node.dataset.deviceHold]: node.value }; save(); }
  });
  root.addEventListener('input', event => {
    const node = event.target;
    if (node.type === 'range' && draft) { draft[node.dataset.config] = Number(node.value); renderSettings(); save(400); }
  });
  root.querySelector('[role="tablist"]').addEventListener('keydown', event => {
    const tabs = all('[role="tab"]'); const index = tabs.indexOf(document.activeElement);
    if (index < 0 || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
    setView(tabs[next].dataset.view, true);
  });

  // --- Live state -------------------------------------------------------------
  function connectEvents() {
    const events = new EventSource('/api/events');
    events.addEventListener('state', event => {
      el('offline').hidden = true;
      snap = JSON.parse(event.data);
      if (!draft || !saving) draft = structuredClone(snap.config);
      render();
    });
    events.addEventListener('toast', event => notifyConnection(JSON.parse(event.data)));
    events.onerror = () => { el('offline').hidden = false; el('connection-summary').textContent = 'Sem conexão com o Pad2Go'; };
  }
  window.addEventListener('beforeunload', stopTest);
  render(); connectEvents();
})();
