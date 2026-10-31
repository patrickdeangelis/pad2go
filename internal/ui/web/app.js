/* Pad2Go interface. State comes from the local pad2go process:
   GET /api/state, live updates over /api/events, input tests over
   /api/players/{n}/input. Settings are saved to config.yaml by the server. */
(() => {
  'use strict';
  const root = document.getElementById('pad2go');
  const el = id => document.getElementById(id);
  const all = query => [...document.querySelectorAll(query)];
  const xboxPadHTML = document.getElementById('test-output-controller')?.innerHTML || '';
  const prefsKey = 'pad2go-ui-v2';
  const ui = { view: 'controllers', detail: null, testing: null, discoveryOpen: false, discoveryKind: null, notifications: true, remapScope: 'global' };
  let snap = null;
  let draft = null;
  let inputStream = null;
  let saveTimer = null;
  let saving = false;
  try {
    const saved = JSON.parse(localStorage.getItem(prefsKey));
    if (saved) ui.notifications = saved.notifications !== false;
  } catch { /* Storage is optional. */ }

  // Behave like a desktop app: no page context menu or text selection
  // outside editable fields.
  document.addEventListener('contextmenu', event => { if (!event.target.closest('input, select')) event.preventDefault(); });

  const paths = {
    gamepad: '<path d="M6 8h12c2 0 3 2 3 4l1 5c.5 3-2 4-4 2l-2-2H8l-2 2c-2 2-4.5 1-4-2l1-5c0-2 1-4 3-4Z"/><path d="M6 11v5m-2.5-2.5h5m7.5-1h.01m2 2h.01"/>',
    orbit: '<circle cx="12" cy="12" r="3"/><ellipse cx="12" cy="12" rx="10" ry="5" transform="rotate(-35 12 12)"/>',
    gear: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3m0 14v3M4.9 4.9l2.1 2.1m10 10 2.1 2.1M2 12h3m14 0h3M4.9 19.1 7 17M17 7l2.1-2.1"/>',
    bluetooth: '<path d="m7 7 10 10-5 4V3l5 4L7 17"/>',
    monitor: '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M12 17v4m-4 0h8"/>',
    chevron: '<path d="m9 6 6 6-6 6"/>',
    info: '<circle cx="12" cy="12" r="9.5"/><path d="M12 11v6m0-9.5h.01"/>',
    check: '<path d="m5 12 4 4L19 6"/>',
    x: '<path d="m6 6 12 12M6 18 18 6"/>',
    battery: '<rect x="2" y="7" width="17" height="10" rx="2.5"/><path d="M22 10.5v3M5 10v4m3-4v4m3-4v4"/>',
    'battery-medium': '<rect x="2" y="7" width="17" height="10" rx="2.5"/><path d="M22 10.5v3M5 10v4m3-4v4"/>',
    'battery-low': '<rect x="2" y="7" width="17" height="10" rx="2.5"/><path d="M22 10.5v3M5 10v4"/>',
    warn: '<path d="M12 3 2 20h20L12 3Z"/><path d="M12 10v4m0 3h.01"/>',
  };
  const icon = name => `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || paths.check}</svg>`;
  const escape = text => String(text).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
  function icons() { all('[data-icon]').forEach(node => { node.outerHTML = icon(node.dataset.icon); }); }
  function persistPrefs() { try { localStorage.setItem(prefsKey, JSON.stringify({ notifications: ui.notifications })); } catch { /* Optional. */ } }
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
    while (container.children.length >= 3) container.firstElementChild.remove();
    const toast = document.createElement('section');
    toast.className = 'toast';
    const image = document.createElement('img'); image.src = 'assets/pad2go-icon.png'; image.alt = '';
    const text = document.createElement('div');
    const title = document.createElement('strong'); title.textContent = `${name} ${connected ? 'conectado' : 'desconectado'}`;
    const body = document.createElement('span'); body.textContent = `Jogador ${player}`;
    text.append(title, body);
    const close = document.createElement('button'); close.type = 'button'; close.setAttribute('aria-label', 'Fechar aviso'); close.innerHTML = icon('x'); close.addEventListener('click', () => toast.remove());
    toast.append(image, text, close); container.append(toast);
    setTimeout(() => toast.remove(), 5000);
  }

  // --- Players --------------------------------------------------------------
  const controllerAsset = kind => ({ ds: 'dualsense', left: 'joycon-l', right: 'joycon-r', pro: 'pro', gc: 'gamecube' }[kind] || 'pro');
  const holdMode = member => draft?.holds?.[member?.addr] || draft?.hold || snap?.config?.holds?.[member?.addr] || snap?.config?.hold || 'Vertical';
  function art(members) {
    const kinds = members.length === 2 ? ['left', 'right'] : [members[0]?.kind || 'pro'];
    const horizontal = members.length === 1 && ['left', 'right'].includes(kinds[0]) && holdMode(members[0]) === 'Horizontal';
    return `<div class="controller-mini ${members.length === 2 ? 'pair' : ''}">${kinds.map(kind => `<img class="${horizontal ? 'sideways' : ''}" src="assets/controllers/${controllerAsset(kind)}.svg" alt="" aria-hidden="true">`).join('')}</div>`;
  }
  const ledPatterns = [0, 1, 3, 7, 15, 9, 5, 13, 6]; // protocol.LEDPattern
  const leds = (player, kind) => {
    const dualSense = kind === 'ds';
    const pattern = dualSense ? [0, 0x04, 0x0a, 0x15, 0x1b, 0x1f, 0x1f, 0x1f, 0x1f][player] : ledPatterns[player];
    const bits = dualSense ? [0, 1, 2, 3, 4] : [0, 1, 2, 3];
    return `<span class="leds${dualSense ? ' dualsense-leds' : ''}" role="img" aria-label="LEDs do jogador ${player}">${bits.map(bit => `<span class="${pattern & (1 << bit) ? 'lit' : ''}"></span>`).join('')}</span>`;
  };
  const lowBattery = member => member.battery === 'low';
  const batteryLabels = { high: 'Alta', medium: 'Média', low: 'Baixa' };
  const batteryIcons = { high: 'battery', medium: 'battery-medium', low: 'battery-low' };
  const batteryText = m => m.batteryPercent != null ? `${m.batteryStatus === 'full' ? '' : '≈ '}${m.batteryPercent}%${m.batteryStatus === 'charging' ? ' · Carregando' : m.batteryStatus === 'full' ? ' · Carregada' : ''}` : m.batteryStatus === 'error' ? 'Falha na bateria' : (batteryLabels[m.battery] || 'Desconhecida');
  const volts = v => `${v.toFixed(2).replace('.', ',')} V`;
  const reportStatus = d => !d ? '' : d.lastValidMillis < 0 ? 'Aguardando relatórios' : d.lastValidMillis > 2000 ? 'Sem relatórios recentes' : d.invalid > 0 ? 'Relatórios inválidos detectados' : 'Recebendo relatórios';
  const diagnosticText = d => `Relatórios: ${d.valid} válidos · ${d.invalid} inválidos · ${d.lastValidMillis < 0 ? 'aguardando entrada' : `último há ${(d.lastValidMillis / 1000).toFixed(1)} s`} · sensores ${d.calibrated ? 'calibrados' : 'sem calibração'}`;
  function updateDeviceDiagnostics(players) {
    all('[data-report-diagnostics]').forEach(node => {
      const [player, member] = node.dataset.reportDiagnostics.split(':').map(Number);
      const diagnostics = players.find(p => p.player === player)?.members[member]?.diagnostics;
      if (diagnostics) node.textContent = diagnosticText(diagnostics);
    });
  }

  let devicesKey = '';
  function renderDevices() {
    const players = snap.players;
    el('player-count').textContent = players.length ? `${players.length} ${players.length === 1 ? 'conectado' : 'conectados'}` : '';
    el('player-badge').hidden = !players.length;
    el('player-badge').textContent = players.length;
    el('controllers-subtitle').textContent = players.length ? snap.output.motionOnly ? 'Enviando movimento aos emuladores.' : 'Prontos para o próximo jogo.' : 'Nenhum controle conectado.';
    if (ui.testing !== null && !players.some(p => p.player === ui.testing)) closeTest();
    if (ui.detail !== null && !players.some(p => p.player === ui.detail)) ui.detail = null;
    // Voltage jitters between snapshots: only the open details show it.
    const shown = players.map(p => ({ ...p, members: p.members.map(m => ({ ...m, volts: ui.detail === p.player ? m.volts.toFixed(2) : 0, diagnostics: m.diagnostics ? { status: reportStatus(m.diagnostics), calibrated: m.diagnostics.calibrated } : null })) }));
    const key = JSON.stringify([shown, ui.detail, snap.config.layout, draft.hold, draft.holds]);
    if (key === devicesKey) { updateDeviceDiagnostics(players); return; }
    devicesKey = key;
    el('device-list').innerHTML = players.length ? players.map(slot => {
      const pair = slot.members.length === 2;
      const low = slot.members.some(lowBattery);
      const warning = slot.members.find(m => m.diagnostics && (m.diagnostics.lastValidMillis > 2000 || m.diagnostics.invalid > 0 || !m.diagnostics.calibrated));
      const status = low ? 'Bateria baixa' : warning ? (warning.diagnostics.lastValidMillis > 2000 || warning.diagnostics.invalid > 0 ? reportStatus(warning.diagnostics) : 'Calibração indisponível') : 'Conectado';
      const single = slot.members.length === 1 && ['left', 'right'].includes(slot.members[0].kind);
      const batteries = slot.members.filter(m => m.battery || m.batteryStatus).map(m => `<span class="battery ${lowBattery(m) || m.batteryStatus === 'error' ? 'low' : ''}" title="${m.batteryPercent != null && m.batteryStatus !== 'full' ? 'Estimativa em intervalos de 10%' : 'Bateria'}">${icon(batteryIcons[m.battery] || 'warn')}${pair ? (m.kind === 'left' ? 'L ' : 'R ') : ''}${batteryText(m)}</span>`).join('');
      const open = ui.detail === slot.player;
      const outputPill = slot.outputOverrideKind && snap.output.available && snap.output.available[slot.outputOverrideKind] === false
        ? `${escape(slot.outputOverride)} indisponível`
        : escape(slot.output);
      return `<div class="row device-row" data-player="${slot.player}">
          <div class="device-icon" aria-hidden="true"><div class="art">${art(slot.members)}</div></div>
          <div class="row-label"><span>Jogador ${slot.player} · ${escape(slot.name)}</span><small class="device-meta"><span class="status ${low || warning ? 'warning' : 'on'}">${status}</span><span class="output-pill">${outputPill}${slot.outputOverride ? ' · manual' : ''}</span>${batteries}${leds(slot.player, slot.members[0]?.kind)}</small></div>
          <div class="row-control"><select class="player-output" data-player-output="${slot.player}" title="Saída: ${escape(slot.output)}" aria-label="Saída do jogador ${slot.player}"><option value="auto">Padrão</option><option value="xbox">Xbox</option><option value="dualsense">DualSense</option><option value="switch">Switch Pro</option><option value="none">Sem saída</option></select><button type="button" class="push" data-test="${slot.player}">Testar</button><button type="button" class="info-button" data-detail="${slot.player}" aria-expanded="${open}" aria-label="Detalhes do jogador ${slot.player}" title="Detalhes">${icon('info')}</button></div>
        </div>${open ? `<div class="device-details">
          <span>${slot.members.map(m => `${escape(m.transport || (m.kind === 'ds' ? 'USB' : 'Bluetooth LE'))} · ${escape(m.addr)}`).join(' · ')}</span>
          ${slot.members.map((m, i) => m.diagnostics ? `<span data-report-diagnostics="${slot.player}:${i}">${diagnosticText(m.diagnostics)}</span>` : '').join('')}
          ${slot.members.some(m => m.battery && m.volts > 0) ? `<span>Bateria estimada pela tensão: ${slot.members.filter(m => m.battery && m.volts > 0).map(m => `${pair ? (m.kind === 'left' ? 'L ' : 'R ') : ''}${volts(m.volts)}`).join(' · ')}</span>` : ''}
          ${single ? `<label class="detail-line">Posição deste Joy-Con<select data-device-hold="${escape(slot.members[0].addr)}"><option value="Vertical">Vertical</option><option value="Horizontal">Horizontal</option></select></label>` : ''}
          <button type="button" class="danger-link" data-disconnect="${slot.player}">Desconectar ${pair ? 'par' : 'controle'}</button>
        </div>` : ''}`;
    }).join('') : '<div class="empty"><strong>Nenhum controle</strong>Segure SYNC em um controle novo ou pressione um botão em um já pareado.</div>';
    all('[data-device-hold]').forEach(node => { node.value = draft.holds?.[node.dataset.deviceHold] || draft.hold; });
    all('[data-player-output]').forEach(node => {
      const slot = players.find(p => p.player === Number(node.dataset.playerOutput));
      node.value = slot?.outputOverrideKind || 'auto';
    });
  }

  async function setDefaultOutput(value, apply) {
    try {
      snap = await api('/api/output', { method: 'POST', body: { backend: value, apply } });
      devicesKey = '';
      render();
      announce(apply ? 'Saída padrão aplicada aos players atuais.' : 'Saída padrão atualizada.');
    } catch (error) {
      announce(`Não foi possível mudar a saída: ${error.message}`, true);
      render();
    }
  }

  async function setPlayerOutput(player, value) {
    try {
      snap = await api(`/api/players/${player}/output`, { method: 'POST', body: { output: value } });
      devicesKey = '';
      render();
      announce(`Saída do jogador ${player} atualizada.`);
    } catch (error) {
      announce(`Não foi possível mudar a saída: ${error.message}`, true);
      render();
    }
  }

  function renderDiscovery() {
    const d = snap.discovery;
    // A controller bonded to the console needs SYNC: show how, once per failure.
    if (d.bonded && !ui.bondedShown && !ui.discoveryOpen) { ui.discoveryOpen = true; ui.discoveryKind = 'nintendo'; }
    ui.bondedShown = Boolean(d.bonded);
    // Close the setup once a controller is ready, unless a lone Joy-Con is
    // still waiting for its partner to join the same player.
    if (d.step === 4 && ui.lastStep !== 4 && ui.discoveryOpen && ui.discoveryKind === 'nintendo') {
      clearTimeout(ui.closeTimer);
      ui.closeTimer = setTimeout(() => {
        const waitingPartner = snap.config.combine && snap.players.some(p => p.members.length === 1 && ['left', 'right'].includes(p.members[0].kind));
        if (snap.discovery.step === 4 && !waitingPartner) { ui.discoveryOpen = false; render(); }
      }, 1500);
    }
    ui.lastStep = d.step;
    el('discovery').hidden = !ui.discoveryOpen;
    el('nintendo-discovery').hidden = ui.discoveryKind !== 'nintendo';
    el('dualsense-discovery').hidden = ui.discoveryKind !== 'dualsense';
    el('open-bluetooth-settings').hidden = snap.platform !== 'Windows';
    el('discovery-steps').innerHTML = ['Procurando', 'Encontrado', 'Conectando', 'Pareando', 'Pronto'].map((name, index) => {
      const cls = index < d.step || (d.step === 4 && index === 4) ? 'done' : index === d.step ? 'current' : '';
      return `<li class="${cls}" ${index === d.step ? 'aria-current="step"' : ''}>${name}</li>`;
    }).join('');
    // An output fault runs motion-only: the search goes on.
    const blocking = snap.fault && snap.fault.kind !== 'output';
    el('discovery-spinner').classList.toggle('idle', Boolean(blocking) || d.failure || d.foreign || d.step === 4 || d.step < 0);
    el('discovery-status').textContent = blocking ? 'Busca pausada: resolva o problema indicado acima.' : d.text || 'A busca é automática. Não é preciso escolher o controle.';
    el('foreign-action').hidden = !d.foreign || snap.config.foreign;
    el('howto').classList.toggle('attention', Boolean(d.bonded));
    el('howto-reconnect').textContent = snap.platform === 'macOS' && !snap.config.hostMac
      ? 'No macOS o Pad2Go não lê o endereço Bluetooth do computador, então o pareamento não fica salvo: segure SYNC a cada conexão (ou informe o endereço em Ajustes → Conexão).'
      : 'Depois do primeiro pareamento, basta apertar qualquer botão para reconectar.';
  }

  function renderSettings() {
    if (!draft.outputRemaps) draft.outputRemaps = {};
    all('[data-config]').forEach(node => {
      const value = draft[node.dataset.config];
      if (node.type === 'checkbox') node.checked = value;
      else if (document.activeElement !== node) node.value = value;
    });
    all('[data-deadzone]').forEach(node => { if (document.activeElement !== node) node.value = draft.deadzones[node.dataset.deadzone]; });
    all('[data-remap]').forEach(node => { node.value = draft.remaps[node.dataset.remap] || 'Default'; });
    all('[data-ds-remap]').forEach(node => { node.value = draft.dualSenseRemaps?.[node.dataset.dsRemap] || 'Default'; });
    const scopeRemaps = draft.outputRemaps[ui.remapScope] || {};
    all('[data-output-remap]').forEach(node => {
      const source = node.dataset.outputRemap;
      if (document.activeElement !== node) node.value = scopeRemaps[source] || 'Default';
      const inherited = ui.remapScope !== 'global' && (!scopeRemaps[source] || scopeRemaps[source] === 'Default')
        ? draft.outputRemaps.global?.[source] : null;
      const hint = node.closest('.remap-field')?.querySelector('[data-remap-inherit]');
      if (hint) hint.textContent = inherited ? `Global: ${outputRemapLabels[inherited] || inherited}` : '';
    });
    all('[data-remap-scope]').forEach(button => button.setAttribute('aria-checked', button.dataset.remapScope === ui.remapScope));
    all('[data-layout]').forEach(button => button.setAttribute('aria-checked', button.dataset.layout === draft.layout));
    el('layout-hint').textContent = draft.layout === 'Switch' ? 'Pela letra: A do Switch → A do Xbox' : 'Pela posição: B do Switch → A do Xbox';
    el('vibration-value').value = `${draft.vibration * 20}%`;
    el('sensitivity-value').value = `${draft.sensitivity} · ${(1 + (draft.sensitivity - 1) / 12).toFixed(2).replace('.', ',')}×`;
    el('connection-notifications').checked = ui.notifications;
  }

  function render() {
    all('[data-view]').forEach(button => { const selected = button.dataset.view === ui.view; button.setAttribute('aria-selected', selected); button.tabIndex = selected ? 0 : -1; el(`panel-${button.dataset.view}`).hidden = !selected; });
    if (!snap) return;
    const mac = snap.platform === 'macOS';
    el('version').textContent = `${snap.platform} · pad2go ${snap.version}`;
    renderSettings();
    el('pending').hidden = !snap.pending && !snap.restarting;
    el('apply-config').disabled = snap.restarting;
    el('apply-config').textContent = snap.restarting ? 'Reiniciando…' : 'Reiniciar conexões';
    el('platform-notice').hidden = !mac;
    [...el('backend').options].forEach(option => { option.disabled = option.value === 'vigem' && snap.platform !== 'Windows' || option.value === 'uinput' && snap.platform !== 'Linux' || (option.value === 'dualsense' || option.value === 'switch' || option.value === 'xbox') && snap.platform === 'macOS'; });
    el('host-mac').placeholder = mac ? 'Necessário para parear' : 'Detectar automaticamente';
    el('output-default').value = snap.output.default;
    const apply = el('apply-output');
    apply.disabled = !snap.players.length;
    apply.title = 'Aplicar aos players atuais (mantém escolhas manuais)';
    el('output-detail').textContent = snap.output.detail;
    el('bluetooth-state').textContent = { on: 'Ativo · busca automática', off: 'Indisponível', idle: 'Não iniciado' }[snap.bluetooth] || '';
    const dsu = snap.dsu;
    const motion = el('motion-state');
    motion.textContent = dsu.running ? 'Ativo' : dsu.error ? 'Falha ao iniciar' : dsu.enabled ? 'Aguardando reinício' : 'Desativado';
    motion.className = `status ${dsu.running ? 'on' : dsu.error ? 'error' : dsu.enabled ? 'warning' : ''}`;
    el('motion-address').textContent = dsu.error ? `${dsu.address} · ${dsu.error}` : dsu.address;
    el('client-count').textContent = dsu.clients;
    const fault = snap.fault;
    el('system-alert').hidden = !fault;
    if (fault) {
      const title = fault.kind === 'bluetooth' ? 'O Bluetooth não está disponível' : fault.kind === 'output' ? 'Sem saida virtual: os jogos não veem os controles' : 'Problema na configuração';
      const reason = fault.kind === 'bluetooth' ? mac ? 'O macOS ainda não permitiu que o Pad2Go use o Bluetooth, ou o Bluetooth está desligado.' : 'Ligue o Bluetooth e confira se há um adaptador compatível com Bluetooth LE.' : fault.kind === 'output' ? snap.platform === 'Windows' ? 'Os controles conectam e podem ser testados, mas falta um backend de saida. Xbox 360 exige um build com cgo e ViGEmBus; DualSense/Switch exigem WinUHid (Windows) ou /dev/uhid (Linux).' : mac ? 'No macOS não há controle virtual: escolha “Automática” ou “Somente movimento” em Ajustes → Conexão → Saída.' : 'Confira o acesso a /dev/uinput.' : '';
      el('system-alert').innerHTML = `<strong>${title}</strong><span>${reason} <span class="technical">(${escape(fault.message)})</span></span><div class="banner-actions"><button type="button" class="push" data-retry>${snap.restarting ? 'Tentando…' : 'Tentar novamente'}</button><button type="button" class="link" data-help="${fault.kind === 'bluetooth' ? 'bluetooth' : 'output'}">Como resolver</button></div>`;
    }
    el('connect').disabled = false;
    const busy = snap.discovery.step >= 1 && snap.discovery.step < 4 && !snap.discovery.failure && !snap.discovery.foreign;
    el('sidebar-status').textContent = fault ? 'Ação necessária' : snap.restarting ? 'Reiniciando…' : busy ? 'Conectando…' : snap.players.length ? `${snap.players.length} ${snap.players.length === 1 ? 'jogador' : 'jogadores'}` : 'Procurando controles';
    renderDevices(); renderDiscovery(); icons();
  }

  // --- Input test -------------------------------------------------------------
  const buttonIds = { up: 0x0001, down: 0x0002, left: 0x0004, right: 0x0008, start: 0x0010, back: 0x0020, l: 0x0100, r: 0x0200, guide: 0x0400, a: 0x1000, b: 0x2000, x: 0x4000, y: 0x8000 };
  const physicalLabels = { MINUS: '−', PLUS: '+', HOME: 'Home', CAPT: 'Captura', C: 'C (Chat)', L_STK: 'L3', R_STK: 'R3', UP: '↑', DOWN: '↓', LEFT: '←', RIGHT: '→', SL_L: 'SL', SR_L: 'SR', SL_R: 'SL', SR_R: 'SR' };
  const physicalOrder = ['A', 'B', 'X', 'Y', 'UP', 'DOWN', 'LEFT', 'RIGHT', 'L', 'R', 'ZL', 'ZR', 'L_STK', 'R_STK', 'MINUS', 'PLUS', 'HOME', 'CAPT', 'MUTE', 'C', 'SL_L', 'SR_L', 'SL_R', 'SR_R', 'GL', 'GR'];
  const number = (value, digits = 2) => value.toFixed(digits).replace('.', ',');
  const physicalKey = (name, kind) => {
    const common = { UP: 'UP', DOWN: 'DOWN', LEFT: 'LEFT', RIGHT: 'RIGHT', L_STK: 'LS', R_STK: 'RS', MINUS: 'SELECT', PLUS: 'START', HOME: 'HOME', CAPT: 'MUTE', C: 'TOUCH', L: 'L', R: 'R', ZL: 'L2', ZR: 'R2', GC_L_CLICK: 'L2', GC_R_CLICK: 'Z', GL: 'L', GR: 'R', SL_L: 'L', SR_L: 'L', SL_R: 'R', SR_R: 'R' };
    if (kind === 'ds') return ({ A: 'EAST', B: 'SOUTH', X: 'NORTH', Y: 'WEST', ...common, L: 'L1', R: 'R1', ZL: 'L2', ZR: 'R2', MINUS: 'SELECT', PLUS: 'START', HOME: 'HOME', CAPT: 'MUTE', MUTE: 'MUTE', PS_L_Touch: 'TOUCH', PS_R_Touch: 'TOUCH', PS_C_Click: 'TOUCH' })[name];
    if (kind === 'gc') return ({ A: 'SOUTH', B: 'WEST', X: 'EAST', Y: 'NORTH', ...common, MINUS: 'SELECT', PLUS: 'START', HOME: 'HOME' })[name];
    if (kind === 'left' || (kind === 'joy-pair' && ['CAPT', 'SL_L', 'SR_L'].includes(name))) return ({ ...common, ZL: 'ZL', CAPT: 'CAPTURE', SL_L: 'SL', SR_L: 'SR' })[name];
    if (kind === 'right') return ({ ...common, A: 'EAST', B: 'SOUTH', X: 'NORTH', Y: 'WEST', ZR: 'ZR', SL_R: 'SL', SR_R: 'SR' })[name];
    if (kind === 'joy-pair' && ['SL_R', 'SR_R'].includes(name)) return ({ ...common, SL_R: 'SL', SR_R: 'SR' })[name];
    if (kind === 'pro') return ({ ...common, ZL: 'ZL', ZR: 'ZR', CAPT: 'CAPTURE' })[name];
    return ({ A: 'EAST', B: 'SOUTH', X: 'NORTH', Y: 'WEST', ...common })[name];
  };
  const svgCache = new Map();
  async function controllerSVG(kind) {
    const file = controllerAsset(kind);
    if (!svgCache.has(file)) svgCache.set(file, fetch(`assets/controllers/${file}.svg`).then(r => { if (!r.ok) throw new Error(`SVG ${file}: ${r.status}`); return r.text(); }).then(svg => svg.replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, '').replace(/<style\b[^>]*>[\s\S]*?<\/style>/gi, '').replace('class="hardware"', 'class="hardware test-hardware"')));
    return svgCache.get(file);
  }
  async function controllerTestArt(members, horizontal = false) {
    const kinds = members.length === 2 ? ['left', 'right'] : [members[0]?.kind || 'pro'];
    return `<div class="hardware-set ${members.length === 2 ? 'pair' : ''} ${horizontal ? 'joy-horizontal' : ''}">${(await Promise.all(kinds.map(controllerSVG))).join('')}</div>`;
  }
  function highlightVirtual(sample) {
    const map = virtualKeyMap[virtualKind];
    const root = el('test-output-controller');
    if (!map || !root) return;
    root.querySelectorAll('.control[data-key]').forEach(node => {
      const key = node.dataset.key;
      let pressed = Boolean(sample.buttons & (map[key] || 0));
      if (key === 'L2' || key === 'ZL') pressed = sample.lt > 0;
      if (key === 'R2' || key === 'ZR') pressed = sample.rt > 0;
      if (key === 'CAPTURE') pressed = (sample.mapped || sample.pressed || []).includes('CAPT');
      if (key === 'MUTE') pressed = (sample.mapped || sample.pressed || []).includes('MUTE');
      node.classList.toggle('is-pressed', pressed);
    });
  }

  function showInput(sample) {
    Object.entries(buttonIds).forEach(([id, bit]) => { const node = el(`pad-${id}`); if (node) node.classList.toggle('active', Boolean(sample.buttons & bit)); });
    const leftStick = el('stick-left');
    const rightStick = el('stick-right');
    if (leftStick) leftStick.parentElement.classList.toggle('active', Boolean(sample.buttons & 0x0040));
    if (rightStick) rightStick.parentElement.classList.toggle('active', Boolean(sample.buttons & 0x0080));
    const pressed = (sample.pressed || []).slice().sort((a, b) => (physicalOrder.indexOf(a) + 99) % 99 - (physicalOrder.indexOf(b) + 99) % 99);
    const physicalNames = ui.testKind === 'ds' ? { Y: 'Square', X: 'Triangle', B: 'Cross', A: 'Circle', L: 'L1', R: 'R1', ZL: 'L2', ZR: 'R2', MINUS: 'Create', PLUS: 'Options', HOME: 'PS', MUTE: 'Mute', PS_C_Click: 'Clique do touchpad' } : {};
    el('touch-test').hidden = ui.testKind !== 'ds';
    const touches = (sample.touch || []).filter(point => point.active);
    el('touch-values').value = touches.length ? touches.map(point => `#${point.id}: ${point.x}, ${point.y}`).join(' · ') : 'Sem contato';
    [0, 1].forEach(i => { const dot = el(`touch-point-${i}`); const point = (sample.touch || [])[i]; dot.toggleAttribute('hidden', !point?.active); if (point?.active) { dot.setAttribute('cx', Math.max(0, Math.min(1919, point.x))); dot.setAttribute('cy', Math.max(0, Math.min(1079, point.y))); } });
    all('#test-controller .control[data-key]').forEach(node => node.classList.toggle('is-pressed', pressed.some(name => physicalKey(name, ui.testKind) === node.dataset.key)));
    const physicalLT = sample.analog ? sample.plt : sample.lt;
    const physicalRT = sample.analog ? sample.prt : sample.rt;
    all('#test-controller .control[data-key="L2"], #test-controller .control[data-key="R2"]').forEach(node => node.classList.toggle('is-pressed', (node.dataset.key === 'L2' ? physicalLT : physicalRT) > 0));
    const pair = ui.testKind === 'joy-pair';
    const sticks = pair ? (sample.physicalKind === 1 ? [['LS', sample.plx, sample.ply]] : sample.physicalKind === 2 ? [['RS', sample.prx, sample.pry]] : [])
      : ui.testKind === 'left' ? [['LS', sample.plx, sample.ply]] : ui.testKind === 'right' ? [['RS', sample.prx, sample.pry]]
      : [['LS', sample.plx, sample.ply], ['RS', sample.prx, sample.pry]];
    sticks.forEach(([key, x, y]) => all(`#test-controller [data-key="${key}"]`).forEach(node => { node.style.transformBox = 'fill-box'; node.style.transformOrigin = 'center'; node.style.transform = `translate(${x * 24}px,${-y * 24}px)`; }));
    all('#test-shoulders [data-input]').forEach(node => node.classList.toggle('is-active', pressed.some(name => physicalKey(name, ui.testKind) === physicalKey(node.dataset.input, ui.testKind))));
    all('#test-shoulders [data-trigger]').forEach(node => node.classList.toggle('is-active', (node.dataset.trigger === 'lt' ? physicalLT : physicalRT) > 0));
    el('pressed-values').value = pressed.length ? pressed.map(name => physicalNames[name] || physicalLabels[name] || name.replace(/_/g, ' ')).join(' · ') : '—';
    if (leftStick) leftStick.style.transform = `translate(${sample.lx * 14}px,${-sample.ly * 14}px)`;
    if (rightStick) rightStick.style.transform = `translate(${sample.rx * 14}px,${-sample.ry * 14}px)`;
    highlightVirtual(sample);
    el('left-values').value = `X ${number(sample.lx)} · Y ${number(sample.ly)}`;
    el('right-values').value = `X ${number(sample.rx)} · Y ${number(sample.ry)}`;
    [['l', sample.lt], ['r', sample.rt]].forEach(([side, raw]) => {
      const value = Math.round(raw / 255 * 100);
      el(`trigger-${side}`).value = value; el(`trigger-${side}-value`).value = `${value}%${sample.analog ? '' : ' · digital'}`;
    });
    el('gyro-values').value = ui.testKind === 'ds' && !sample.motionCalibrated ? 'Calibração indisponível' : sample.gyro.map(v => number(v, 1)).join(' · ');
    if (!inputNoteUntil || Date.now() > inputNoteUntil) { inputNoteUntil = 0; el('input-state').textContent = 'Recebendo entradas.'; }
  }
  let inputNoteUntil = 0;
  // A short message (e.g. after Vibrar) that live input doesn't overwrite.
  function inputNote(text) { el('input-state').textContent = text; inputNoteUntil = Date.now() + 2500; }
  function resetInput() { showInput({ buttons: 0, pressed: [], lx: 0, ly: 0, rx: 0, ry: 0, lt: 0, rt: 0, analog: false, gyro: [0, 0, 0] }); el('input-state').textContent = 'Aguardando entradas.'; }
  function stopStream() { inputStream?.close(); inputStream = null; }
  function closeTest() {
    stopStream();
    const player = ui.testing;
    ui.testing = null;
    if (el('test-dialog').open) el('test-dialog').close();
    document.querySelector(`[data-test="${player}"]`)?.focus();
  }
  async function openTest(player) {
    stopStream(); ui.testing = player;
    const slot = snap.players.find(p => p.player === player);
    const kinds = slot?.members.map(m => m.kind) || [];
    ui.testKind = kinds.length === 2 ? 'joy-pair' : kinds[0] || '';
    resetInput();
    const horizontal = kinds.length === 1 && ['left', 'right'].includes(kinds[0]) && holdMode(slot.members[0]) === 'Horizontal';
    const deviceName = slot?.members.length === 2 ? 'Par de Joy-Cons' : slot?.members[0]?.name || slot?.name || 'Controle';
    el('test-title').textContent = `Teste de entrada · ${deviceName}`;
    el('test-device-name').textContent = deviceName;
    el('test-shoulders').innerHTML = testShoulders(kinds);
    el('test-controller').innerHTML = await controllerTestArt(slot?.members || [], horizontal);
    el('test-controller').className = `test-controller ${kinds.length === 2 ? 'joy-pair' : kinds[0] || ''}${horizontal ? ' joy-horizontal' : ''}`;
    el('test-mapping').textContent = mappingText(slot);
    await renderVirtualOutput(slot);
    inputStream = new EventSource(`/api/players/${player}/input`);
    inputStream.addEventListener('input', event => showInput(JSON.parse(event.data)));
    inputStream.onerror = () => { if (ui.testing === player) el('input-state').textContent = 'Sem entradas: o controle desconectou?'; };
    el('test-dialog').showModal();
  }
  let virtualKind = 'xbox';
  const virtualKeyMap = {
    dualsense: {
      SOUTH: 0x1000, EAST: 0x2000, WEST: 0x4000, NORTH: 0x8000,
      L1: 0x0100, R1: 0x0200, SELECT: 0x0020, START: 0x0010, HOME: 0x0400,
      UP: 0x0001, DOWN: 0x0002, LEFT: 0x0004, RIGHT: 0x0008,
      LS: 0x0040, RS: 0x0080,
    },
    switch: {
      SOUTH: 0x1000, EAST: 0x2000, WEST: 0x4000, NORTH: 0x8000,
      L: 0x0100, R: 0x0200, SELECT: 0x0020, START: 0x0010, HOME: 0x0400,
      UP: 0x0001, DOWN: 0x0002, LEFT: 0x0004, RIGHT: 0x0008,
      LS: 0x0040, RS: 0x0080,
    },
  };

  async function renderVirtualOutput(slot) {
    const effective = slot?.outputKind || 'none';
    const requested = slot?.outputOverrideKind || effective;
    virtualKind = requested === 'auto' ? effective : requested;
    const root = el('test-output-controller');
    const labels = {
      xbox: { heading: 'Xbox 360', l: 'LB', r: 'RB', back: 'Back', guide: '\u2302', start: 'Start', y: 'Y', x: 'X', b: 'B', a: 'A' },
      dualsense: { heading: 'DualSense' },
      switch: { heading: 'Switch Pro' },
      none: { heading: 'Sem saída virtual' },
    };
    const set = labels[virtualKind] || labels.none;
    const unavailable = requested !== effective && effective === 'none' ? ' (indisponível)' : '';
    el('test-output-heading').textContent = `Saída virtual · ${set.heading}${unavailable}`;
    el('test-output-heading').dataset.kind = virtualKind;

    if (virtualKind === 'none') {
      root.className = 'input-pad';
      root.innerHTML = '<div class="virtual-empty">Sem saída virtual: os jogos não recebem um controle virtual neste player.</div>';
      return;
    }
    if (virtualKind === 'xbox') {
      root.className = 'input-pad';
      root.innerHTML = xboxPadHTML;
      el('pad-l').textContent = set.l;
      el('pad-r').textContent = set.r;
      el('pad-back').textContent = set.back;
      el('pad-guide').textContent = set.guide;
      el('pad-start').textContent = set.start;
      el('pad-y').textContent = set.y;
      el('pad-x').textContent = set.x;
      el('pad-b').textContent = set.b;
      el('pad-a').textContent = set.a;
      return;
    }

    const file = virtualKind === 'switch' ? 'pro' : 'ds';
    let svg = await controllerSVG(file);
    if (virtualKind === 'switch') {
      // The virtual target is a Switch 1 Pro Controller (057E:2009), which has
      // Capture but not the Switch 2 Chat/C button present in the shared art.
      svg = svg.replace(/<g class="control" data-key="TOUCH"[\s\S]*?<\/g>/, '');
    }
    svg = svg.replace(/id="([^"]+)"/g, 'id="vo-$1"').replace(/url\(#([^)]+)\)/g, 'url(#vo-$1)');
    root.className = 'input-pad virtual-hardware';
    root.innerHTML = `<div class="hardware-set virtual-set">${svg}</div>`;
  }

  function mappingText(slot) {
    const members = slot?.members || [];
    const outputKind = slot?.outputOverrideKind || slot?.outputKind || 'none';
    if (outputKind === 'dualsense') {
      return 'Posicional: B/A/Y/X → Cross/Circle/Square/Triangle · L/R → L1/R1 · ZL/ZR → L2/R2 · touch e movimento são repassados.';
    }
    if (outputKind === 'switch') {
      return 'Posicional: B/A/Y/X → B/A/Y/X · L/R → L/R · ZL/ZR → ZL/ZR · movimento via IMU.';
    }
    if (outputKind === 'none') {
      return 'Sem saída virtual: os botões vão apenas para o DSU.';
    }
    const kinds = members.map(member => member.kind);
    const kind = kinds.length === 2 ? 'joy-pair' : kinds[0] || '';
    const byLabel = (draft?.layout || snap.config.layout) === 'Switch';
    const standardFaces = byLabel ? 'A → A · B → B · X → X · Y → Y' : 'A → B · B → A · X → Y · Y → X';
    if (kind === 'ds') {
      return 'Cross → A · Circle → B · Square → X · Triangle → Y · L1/R1 → LB/RB · L2/R2 → LT/RT';
    }
    if (kind === 'gc') return byLabel
      ? 'A → A · B → B · X → X · Y → Y · Z → RT'
      : 'A → A · B → X · X → B · Y → Y · Z → RT';
    if (kind === 'joy-pair') return `Par combinado · L/R → LB/RB · ZL/ZR → LT/RT · ${standardFaces}`;
    if (kind === 'left' || kind === 'right') {
      const member = members[0];
      const hold = holdMode(member);
      const sideways = hold === 'Horizontal';
      if (kind === 'left') {
        const directions = sideways
          ? (byLabel ? '↑/↓/←/→ → Y/A/B/X' : '↑/↓/←/→ → X/B/A/Y')
          : (byLabel ? '↑/↓/←/→ → X/B/Y/A' : '↑/↓/←/→ → Y/A/X/B');
        const shoulders = sideways ? 'SL/SR → LT/RT' : 'L → LB · ZL → LT';
        return `Joy-Con ${sideways ? 'horizontal' : 'vertical'} · ${shoulders} · ${directions}`;
      }
      const faces = sideways
        ? (byLabel ? 'A/B/X/Y → B/Y/A/X' : 'A/B/X/Y → A/X/B/Y')
        : standardFaces;
      const shoulders = sideways ? 'SL/SR → LT/RT' : 'R → RB · ZR → RT';
      return `Joy-Con ${sideways ? 'horizontal' : 'vertical'} · ${shoulders} · ${faces}`;
    }
    return `L/R → LB/RB · ZL/ZR → LT/RT · ${standardFaces}`;
  }  function testShoulders(kinds) {
    // Joy-Con shoulder inputs are drawn on each physical controller. A second
    // row of floating L/R/ZL/ZR pills made them look detached and duplicated.
    if (kinds.length && kinds.every(kind => kind === 'left' || kind === 'right')) return '';
    let left = [['L', 'L'], ['ZL', 'ZL']];
    let right = [['R', 'R'], ['ZR', 'ZR']];
    if (kinds[0] === 'ds') {
      left = [['L', 'L1'], ['lt', 'L2']]; right = [['R', 'R1'], ['rt', 'R2']];
    } else if (kinds[0] === 'gc') {
      left = [['L', 'L'], ['lt', 'L2']]; right = [['R', 'R'], ['rt', 'R2'], ['GC_R_CLICK', 'Z']];
    } else if (kinds.length === 1 && kinds[0] === 'left') {
      right = [];
    } else if (kinds.length === 1 && kinds[0] === 'right') {
      left = [];
    }
    const group = (side, entries) => `<div class="shoulder-side ${side}">${entries.map(([key, label]) => key === 'lt' || key === 'rt' ? `<span data-trigger="${key}">${label}</span>` : `<span data-input="${key}">${label}</span>`).join('')}</div>`;
    return group('left', left) + group('right', right);
  }
  el('test-dialog').addEventListener('close', () => { if (ui.testing !== null) { stopStream(); ui.testing = null; } });

  // --- Settings -----------------------------------------------------------------
  function validate(node) {
    const key = node.dataset.config;
    let message = '';
    if (key === 'host' && !node.value.trim()) message = 'Informe o endereço do servidor.';
    if (key === 'port' && (!node.value || !Number.isInteger(Number(node.value)) || Number(node.value) < 1 || Number(node.value) > 65535)) message = 'Use uma porta entre 1 e 65535.';
    if (key === 'hostMac' && node.value.trim() && !/^(?:[a-f\d]{12}|(?:[a-f\d]{2}:){5}[a-f\d]{2}|(?:[a-f\d]{2}-){5}[a-f\d]{2})$/i.test(node.value.trim())) message = 'Use AA:BB:CC:DD:EE:FF.';
    if (node.hasAttribute('data-deadzone') && (!node.value || !Number.isFinite(Number(node.value)) || Number(node.value) < 0 || Number(node.value) > 50)) message = 'Use um valor entre 0 e 50.';
    node.setAttribute('aria-invalid', Boolean(message));
    // Show the message in the field's own row, next to its label.
    let error = el(`${key}-error`);
    if (!error) {
      const label = node.closest('.row')?.querySelector('.row-label');
      error = label?.querySelector('.field-error');
      if (label && !error) { error = document.createElement('small'); error.className = 'field-error'; label.append(error); }
    }
    if (error) error.textContent = message;
    if (message) announce(message, true);
    return !message;
  }
  function save(delay = 0) {
    clearTimeout(saveTimer);
    saving = true;
    saveTimer = setTimeout(async () => {
      try {
        snap = await api('/api/config', { method: 'PUT', body: draft });
        draft = structuredClone(snap.config);
        announce('');
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
    try { await api('/api/restart', { method: 'POST' }); closeTest(); announce('Conexões reiniciadas. Pressione um botão nos controles para reconectar.'); } catch (error) { announce(`Não foi possível reiniciar: ${error.message}`, true); }
  }

  // --- Diagnostics ------------------------------------------------------------------
  const help = {
    bluetooth: () => snap.platform === 'macOS' ? 'Na primeira execução o macOS pergunta se o Pad2Go pode usar o Bluetooth: clique em Permitir. Se você recusou, ative o Pad2Go em Ajustes do Sistema → Privacidade e Segurança → Bluetooth. Confira se o Bluetooth está ligado e clique em “Tentar novamente”.' : 'Ligue o Bluetooth nas configurações do computador e confirme que há um adaptador compatível com Bluetooth LE. Depois, clique em “Tentar novamente”.',
    output: () => snap.config.backend === 'dualsense' || snap.config.backend === 'switch' ? `A saida ${snap.config.backend === 'switch' ? 'Switch Pro' : 'DualSense'} virtual precisa do driver <a href="https://github.com/cgutman/WinUHid" target="_blank" rel="noreferrer">WinUHid</a> no Windows, ou de /dev/uhid no Linux. Instale o driver e clique em Tentar novamente. Em emuladores de Switch, escolha o controle como Pro Controller no driver SDL2; se o jogo detectar entradas duplicadas, use o HidHide para ocultar o controle fisico.` : snap.platform === 'macOS' ? 'O macOS não permite criar controles virtuais. Em Ajustes → Conexão → Saída, escolha “Automática” ou “Somente movimento” e reinicie as conexões; o movimento continua disponível para emuladores via DSU.' : snap.platform === 'Windows' ? 'O instalador do Pad2Go já instala o ViGEmBus. Se você usou o pad2go.exe avulso, instale o <a href="https://github.com/nefarius/ViGEmBus/releases/latest" target="_blank" rel="noreferrer">ViGEmBus 1.22</a> e clique em Tentar novamente. Se o jogo detectar entradas duplicadas, use o <a href="https://github.com/nefarius/HidHide" target="_blank" rel="noreferrer">HidHide</a> para ocultar o controle físico.' : 'Carregue o módulo com <code>sudo modprobe uinput</code>, confira se /dev/uinput existe e dê acesso ao seu usuário com uma regra udev.',
    mac: () => 'Informe o endereço Bluetooth do computador em Ajustes → Conexão. Sem ele a conexão funciona, mas o pareamento para reconectar com um toque não é salvo.',
  };
  function showHelp(button) {
    const host = button.closest('.row, .banner');
    const old = host.parentElement.querySelector(`.help[data-for="${button.dataset.help}"]`);
    if (old) { old.remove(); return; }
    const paragraph = document.createElement('p'); paragraph.className = 'help'; paragraph.dataset.for = button.dataset.help;
    paragraph.innerHTML = help[button.dataset.help]();
    host.after(paragraph);
  }
  async function diagnose() {
    try {
      const rows = await api('/api/diagnostics');
      el('diagnostic-results').innerHTML = rows.map(row => `<div class="row"><span class="row-icon ${row.ok ? '' : 'missing'}">${icon(row.ok ? 'check' : 'warn')}</span><div class="row-label"><span>${escape(row.name)}</span><small>${escape(row.detail)}</small></div>${!row.ok && row.help ? `<div class="row-control"><button type="button" class="link" data-help="${escape(row.help)}">Como resolver</button></div>` : ''}</div>`).join('');
    } catch (error) { announce(`Não foi possível verificar: ${error.message}`, true); }
  }

  // --- Events -------------------------------------------------------------------------
  const titles = { controllers: 'Controles', motion: 'Movimento', settings: 'Ajustes' };
  el('main-content').addEventListener('scroll', () => el('toolbar').classList.toggle('scrolled', el('main-content').scrollTop > 2), { passive: true });
  function setView(view, focus = false) { ui.view = view; el('toolbar-title').textContent = titles[view]; announce(''); render(); el('main-content').scrollTop = 0; if (view === 'settings') diagnose(); if (focus) el(`tab-${view}`).focus(); }
  const remapLabels = { Default: 'Padrão', None: 'Sem ação', MINUS: '−', PLUS: '+', L_STK: 'Clique analógico L', R_STK: 'Clique analógico R', UP: 'Cima', DOWN: 'Baixo', LEFT: 'Esquerda', RIGHT: 'Direita', HOME: 'Home', CAPT: 'Captura' };
  const remapValues = ['Default', 'None', 'A', 'B', 'X', 'Y', 'L', 'R', 'ZL', 'ZR', 'MINUS', 'PLUS', 'L_STK', 'R_STK', 'UP', 'DOWN', 'LEFT', 'RIGHT', 'HOME', 'CAPT'];
  el('remaps').innerHTML = [['home', 'Home'], ['capt', 'Captura'], ['c', 'C (GameCube)'], ['gl', 'GL'], ['gr', 'GR'], ['sll', 'SL do Joy-Con L'], ['srl', 'SR do Joy-Con L'], ['slr', 'SL do Joy-Con R'], ['srr', 'SR do Joy-Con R']].map(([key, name]) => `<div class="row"><div class="row-label"><label for="remap-${key}">${name}</label></div><div class="row-control"><select id="remap-${key}" data-remap="${key}">${remapValues.map(value => `<option value="${value}">${remapLabels[value] || value}</option>`).join('')}</select></div></div>`).join('');

  const outputRemapLabels = {
    Default: 'Padrão', None: 'Sem ação',
    A: 'A', B: 'B', X: 'X', Y: 'Y',
    UP: 'Direcional ↑', DOWN: 'Direcional ↓', LEFT: 'Direcional ←', RIGHT: 'Direcional →',
    L: 'L (bumper)', R: 'R (bumper)', ZL: 'ZL (gatilho)', ZR: 'ZR (gatilho)',
    MINUS: '− / Create', PLUS: '+ / Options', HOME: 'Home / PS', CAPT: 'Captura', C: 'C (Chat)',
    MUTE: 'Mudo', L_STK: 'Clique L3', R_STK: 'Clique R3',
    SL_L: 'SL (Joy-Con L)', SR_L: 'SR (Joy-Con L)', SL_R: 'SL (Joy-Con R)', SR_R: 'SR (Joy-Con R)',
    GL: 'GL', GR: 'GR',
    PS_L_Touch: 'Touch esquerdo', PS_R_Touch: 'Touch direito', PS_C_Click: 'Clique do touchpad',
    GC_L_CLICK: 'Clique L (GameCube)', GC_R_CLICK: 'Clique R (GameCube)',
  };
  const outputRemapTargets = ['Default', 'None', 'A', 'B', 'X', 'Y', 'UP', 'DOWN', 'LEFT', 'RIGHT', 'L', 'R', 'ZL', 'ZR', 'MINUS', 'PLUS', 'HOME', 'CAPT', 'C', 'MUTE', 'L_STK', 'R_STK', 'PS_C_Click', 'SL_L', 'SR_L', 'SL_R', 'SR_R', 'GL', 'GR'];
  const outputRemapSources = ['A', 'B', 'X', 'Y', 'UP', 'DOWN', 'LEFT', 'RIGHT', 'L', 'R', 'ZL', 'ZR', 'MINUS', 'PLUS', 'HOME', 'CAPT', 'C', 'MUTE', 'L_STK', 'R_STK', 'SL_L', 'SR_L', 'SL_R', 'SR_R', 'GL', 'GR', 'PS_L_Touch', 'PS_R_Touch', 'PS_C_Click', 'GC_L_CLICK', 'GC_R_CLICK'];
  el('output-remaps').innerHTML = outputRemapSources.map(source => `
    <div class="remap-field">
      <span class="remap-source"><label for="out-remap-${source}">${escape(outputRemapLabels[source] || source)}</label><small data-remap-inherit="${source}"></small></span>
      <select id="out-remap-${source}" data-output-remap="${source}">${outputRemapTargets.map(value => `<option value="${value}">${escape(outputRemapLabels[value] || value)}</option>`).join('')}</select>
    </div>`).join('');
  const dsTargets = [['Default','Padrão'],['None','Sem ação'],['B','A'],['A','B'],['Y','X'],['X','Y'],['L','LB'],['R','RB'],['ZL','LT'],['ZR','RT'],['MINUS','View'],['PLUS','Menu'],['L_STK','Analógico esquerdo'],['R_STK','Analógico direito'],['UP','Direcional cima'],['DOWN','Direcional baixo'],['LEFT','Direcional esquerda'],['RIGHT','Direcional direita'],['HOME','Xbox']];
  el('dualsense-remaps').innerHTML = [['cross','Cruz'],['circle','Círculo'],['square','Quadrado'],['triangle','Triângulo'],['l1','L1'],['r1','R1'],['l2','L2'],['r2','R2'],['l3','L3'],['r3','R3'],['create','Criar'],['options','Opções'],['ps','PS'],['touch','Clique do touchpad'],['up','Direcional cima'],['down','Direcional baixo'],['left','Direcional esquerda'],['right','Direcional direita']].map(([key,name]) => `<div class="row"><div class="row-label"><label for="ds-remap-${key}">${name}</label></div><div class="row-control"><select id="ds-remap-${key}" data-ds-remap="${key}">${dsTargets.map(([value,label]) => `<option value="${value}">${label}</option>`).join('')}</select></div></div>`).join('');

  document.addEventListener('click', async event => {
    const button = event.target.closest('button'); if (!button || button.disabled) return;
    if (button.dataset.view) setView(button.dataset.view);
    else if (button.id === 'connect' || button.id === 'show-howto') { ui.discoveryOpen = true; ui.discoveryKind = null; render(); el('discovery').scrollIntoView({ block: 'nearest', behavior: 'smooth' }); }
    else if (button.dataset.connectKind) { ui.discoveryKind = button.dataset.connectKind; render(); }
    else if (button.id === 'open-bluetooth-settings') { try { await api('/api/bluetooth/settings', { method: 'POST' }); } catch (error) { announce(`Não foi possível abrir as configurações: ${error.message}`, true); } }
    else if (button.id === 'hide-discovery') { ui.discoveryOpen = false; render(); el('connect').focus(); }
    else if (button.id === 'accept-foreign') { draft.foreign = true; clearTimeout(saveTimer); try { snap = await api('/api/config', { method: 'PUT', body: draft }); await restart(); } catch (error) { announce(`Não foi possível salvar: ${error.message}`, true); } }
    else if (button.id === 'apply-config' || button.hasAttribute('data-retry')) await restart();
    else if (button.dataset.detail) { const player = Number(button.dataset.detail); ui.detail = ui.detail === player ? null : player; render(); document.querySelector(`[data-detail="${player}"]`)?.focus(); }
    else if (button.dataset.test) openTest(Number(button.dataset.test));
    else if (button.id === 'close-test') closeTest();
    else if (button.dataset.disconnect) {
      const player = Number(button.dataset.disconnect);
      try { await api(`/api/players/${player}/disconnect`, { method: 'POST' }); ui.detail = null; announce(`Jogador ${player} desconectado. Os demais mantêm seus números.`); } catch (error) { announce(`Não foi possível desconectar: ${error.message}`, true); }
    }
    else if (button.dataset.layout) { draft.layout = button.dataset.layout; render(); save(); }
    else if (button.dataset.remapScope) { ui.remapScope = button.dataset.remapScope; renderSettings(); }
    else if (button.id === 'reset-output-remaps') {
      draft.outputRemaps = { ...(draft.outputRemaps || {}), [ui.remapScope]: {} };
      renderSettings(); save();
    }
    else if (button.id === 'rumble') {
      try { await api(`/api/players/${ui.testing}/rumble`, { method: 'POST' }); inputNote('Vibração enviada ao controle.'); } catch (error) { inputNote(`Não foi possível vibrar: ${error.message}`); }
    }
    else if (button.id === 'copy-address') { const address = snap.dsu.address; try { await navigator.clipboard.writeText(address); el('copy-result').textContent = `Copiado: ${address}`; } catch { el('copy-result').textContent = `Copie: ${address}`; } }
    else if (button.dataset.help) showHelp(button);
    else if (button.id === 'check-system') { await diagnose(); announce('Verificação concluída.'); }
    else if (button.id === 'apply-output') { await setDefaultOutput(el('output-default').value, true); }
  });
  document.addEventListener('change', event => {
    const node = event.target;
    if (node.id === 'connection-notifications') { ui.notifications = node.checked; persistPrefs(); if (!node.checked) el('connection-toasts').replaceChildren(); return; }
    if (node.id === 'output-default') { setDefaultOutput(node.value, false); return; }
    if (node.dataset.playerOutput) { setPlayerOutput(Number(node.dataset.playerOutput), node.value); return; }
    if (!draft) return;
    if (node.hasAttribute('data-output-remap')) {
      const source = node.dataset.outputRemap;
      const scope = ui.remapScope;
      const table = { ...(draft.outputRemaps?.[scope] || {}) };
      if (node.value === 'Default') delete table[source];
      else table[source] = node.value;
      draft.outputRemaps = { ...(draft.outputRemaps || {}), [scope]: table };
      save();
      return;
    }
    if (node.dataset.config && node.type !== 'range') {
      if (!validate(node)) return;
      const key = node.dataset.config;
      draft[key] = node.type === 'checkbox' ? node.checked : ['port', 'max', 'dualSenseBrightness'].includes(key) ? Number(node.value) : node.value.trim();
      save();
    }
    if (node.hasAttribute('data-deadzone')) { if (!validate(node)) return; draft.deadzones[node.dataset.deadzone] = Number(node.value); save(); }
    if (node.hasAttribute('data-remap')) { draft.remaps[node.dataset.remap] = node.value; save(); }
    if (node.hasAttribute('data-ds-remap')) { draft.dualSenseRemaps = { ...draft.dualSenseRemaps, [node.dataset.dsRemap]: node.value }; save(); }
    if (node.dataset.deviceHold) { draft.holds = { ...draft.holds, [node.dataset.deviceHold]: node.value }; save(); }
  });
  document.addEventListener('input', event => {
    const node = event.target;
    if (node.type === 'range' && draft) { draft[node.dataset.config] = Number(node.value); renderSettings(); save(400); }
  });
  root.querySelector('[role="tablist"]').addEventListener('keydown', event => {
    const tabs = all('[role="tab"]'); const index = tabs.indexOf(document.activeElement);
    if (index < 0 || !['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + tabs.length) % tabs.length;
    setView(tabs[next].dataset.view, true);
  });

  // --- Live state -----------------------------------------------------------------------
  function connectEvents() {
    const events = new EventSource('/api/events');
    events.addEventListener('state', event => {
      el('offline').hidden = true;
      snap = JSON.parse(event.data);
      if (!draft || !saving) draft = structuredClone(snap.config);
      render();
    });
    events.addEventListener('toast', event => notifyConnection(JSON.parse(event.data)));
    events.onerror = () => { el('offline').hidden = false; el('sidebar-status').textContent = 'Sem conexão'; };
  }
  window.addEventListener('beforeunload', stopStream);
  // Refresh diagnostics while a DualSense is connected. HID does not expose RSSI
  // or reports lost before reaching Windows, so these are receive counts only.
  setInterval(async () => {
    if (!snap?.players.some(p => p.members.some(m => m.diagnostics))) return;
    try { const current = await api('/api/state'); if (snap) { snap = current; renderDevices(); } } catch { /* EventSource reports disconnects. */ }
  }, 2000);
  icons(); render(); connectEvents();
})();
