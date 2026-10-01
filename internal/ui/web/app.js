/* Pad2Go interface. State comes from the local pad2go process:
   GET /api/state, live updates over /api/events, input tests over
   /api/players/{n}/input. Settings are saved to config.yaml by the server. */
(() => {
  'use strict';
  const root = document.getElementById('pad2go');
  const el = id => document.getElementById(id);
  const all = query => [...document.querySelectorAll(query)];
  const prefsKey = 'pad2go-ui-v2';
  const ui = { view: 'controllers', detail: null, testing: null, discoveryOpen: false, notifications: true };
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
  function joy(right = false) { return `<div class="joy ${right ? 'right' : ''}"><span class="stick"></span>${[1, 2, 3, 4].map(i => `<span class="hw-key k${i}"></span>`).join('')}</div>`; }
  function art(members) {
    if (members.length === 2) return joy() + joy(true);
    const kind = members[0].kind;
    if (kind === 'left' || kind === 'right') return joy(kind === 'right');
    return `<div class="pro ${kind === 'gc' ? 'gamecube' : ''}"><span class="stick"></span><span class="stick second"></span>${[1, 2, 3, 4].map(i => `<span class="hw-key k${i}"></span>`).join('')}</div>`;
  }
  const ledPatterns = [0, 1, 3, 7, 15, 9, 5, 13, 6]; // protocol.LEDPattern
  const leds = player => `<span class="leds" role="img" aria-label="LEDs do jogador ${player}">${[0, 1, 2, 3].map(bit => `<span class="${ledPatterns[player] & (1 << bit) ? 'lit' : ''}"></span>`).join('')}</span>`;
  const lowBattery = member => member.battery >= 0 && member.battery <= 15;

  let devicesKey = '';
  function renderDevices() {
    const players = snap.players;
    el('player-count').textContent = players.length ? `${players.length} ${players.length === 1 ? 'conectado' : 'conectados'}` : '';
    el('player-badge').hidden = !players.length;
    el('player-badge').textContent = players.length;
    el('controllers-subtitle').textContent = players.length ? snap.output.motionOnly ? 'Enviando movimento aos emuladores.' : 'Prontos para o próximo jogo.' : 'Nenhum controle conectado.';
    if (ui.testing !== null && !players.some(p => p.player === ui.testing)) closeTest();
    if (ui.detail !== null && !players.some(p => p.player === ui.detail)) ui.detail = null;
    const key = JSON.stringify([players, ui.detail, snap.config.layout, draft.hold, draft.holds]);
    if (key === devicesKey) return;
    devicesKey = key;
    el('device-list').innerHTML = players.length ? players.map(slot => {
      const pair = slot.members.length === 2;
      const low = slot.members.some(lowBattery);
      const single = slot.members.length === 1 && ['left', 'right'].includes(slot.members[0].kind);
      const batteries = slot.members.filter(m => m.battery >= 0).map(m => `<span class="battery ${lowBattery(m) ? 'low' : ''}">${icon(lowBattery(m) ? 'battery-low' : 'battery')}${pair ? (m.kind === 'left' ? 'L ' : 'R ') : ''}${m.battery}%</span>`).join('');
      const open = ui.detail === slot.player;
      return `<div class="row device-row" data-player="${slot.player}">
          <div class="device-icon" aria-hidden="true"><div class="art">${art(slot.members)}</div></div>
          <div class="row-label"><span>Jogador ${slot.player} · ${escape(slot.name)}</span><small class="device-meta"><span class="status ${low ? 'warning' : 'on'}">${low ? 'Bateria baixa' : 'Conectado'}</span>${batteries}${leds(slot.player)}</small></div>
          <div class="row-control"><button type="button" class="push" data-test="${slot.player}">Testar</button><button type="button" class="info-button" data-detail="${slot.player}" aria-expanded="${open}" aria-label="Detalhes do jogador ${slot.player}" title="Detalhes">${icon('info')}</button></div>
        </div>${open ? `<div class="device-details">
          <span>Bluetooth LE · ${slot.members.map(m => escape(m.addr)).join(' · ')}${slot.members.some(m => m.battery >= 0) ? ' · bateria estimada pela tensão' : ''}</span>
          ${single ? `<label class="detail-line">Posição deste Joy-Con<select data-device-hold="${escape(slot.members[0].addr)}"><option value="Vertical">Vertical</option><option value="Horizontal">Horizontal</option></select></label>` : ''}
          <button type="button" class="danger-link" data-disconnect="${slot.player}">Desconectar ${pair ? 'par' : 'controle'}</button>
        </div>` : ''}`;
    }).join('') : '<div class="empty"><strong>Nenhum controle</strong>Segure SYNC em um controle novo ou pressione um botão em um já pareado.</div>';
    all('[data-device-hold]').forEach(node => { node.value = draft.holds?.[node.dataset.deviceHold] || draft.hold; });
  }

  function renderDiscovery() {
    const d = snap.discovery;
    // A controller bonded to the console needs SYNC: show how, once per failure.
    if (d.bonded && !ui.bondedShown) ui.discoveryOpen = true;
    ui.bondedShown = Boolean(d.bonded);
    // Close the setup once a controller is ready, unless a lone Joy-Con is
    // still waiting for its partner to join the same player.
    if (d.step === 4 && ui.lastStep !== 4 && ui.discoveryOpen) {
      clearTimeout(ui.closeTimer);
      ui.closeTimer = setTimeout(() => {
        const waitingPartner = snap.config.combine && snap.players.some(p => p.members.length === 1 && ['left', 'right'].includes(p.members[0].kind));
        if (snap.discovery.step === 4 && !waitingPartner) { ui.discoveryOpen = false; render(); }
      }, 1500);
    }
    ui.lastStep = d.step;
    el('discovery').hidden = !ui.discoveryOpen;
    el('discovery-steps').innerHTML = ['Procurando', 'Encontrado', 'Conectando', 'Pareando', 'Pronto'].map((name, index) => {
      const cls = index < d.step || (d.step === 4 && index === 4) ? 'done' : index === d.step ? 'current' : '';
      return `<li class="${cls}" ${index === d.step ? 'aria-current="step"' : ''}>${name}</li>`;
    }).join('');
    el('discovery-spinner').classList.toggle('idle', Boolean(snap.fault) || d.failure || d.foreign || d.step === 4 || d.step < 0);
    el('discovery-status').textContent = snap.fault ? 'Busca pausada: resolva o problema indicado acima.' : d.text || 'A busca é automática. Não é preciso escolher o controle.';
    el('foreign-action').hidden = !d.foreign || snap.config.foreign;
    el('howto').classList.toggle('attention', Boolean(d.bonded));
    el('howto-reconnect').textContent = snap.platform === 'macOS' && !snap.config.hostMac
      ? 'No macOS o Pad2Go não lê o endereço Bluetooth do computador, então o pareamento não fica salvo: segure SYNC a cada conexão (ou informe o endereço em Ajustes → Conexão).'
      : 'Depois do primeiro pareamento, basta apertar qualquer botão para reconectar.';
  }

  function renderSettings() {
    all('[data-config]').forEach(node => {
      const value = draft[node.dataset.config];
      if (node.type === 'checkbox') node.checked = value;
      else if (document.activeElement !== node) node.value = value;
    });
    all('[data-deadzone]').forEach(node => { if (document.activeElement !== node) node.value = draft.deadzones[node.dataset.deadzone]; });
    all('[data-remap]').forEach(node => { node.value = draft.remaps[node.dataset.remap] || 'Default'; });
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
    [...el('backend').options].forEach(option => { option.disabled = option.value === 'vigem' && snap.platform !== 'Windows' || option.value === 'uinput' && snap.platform !== 'Linux'; });
    el('host-mac').placeholder = mac ? 'Necessário para parear' : 'Detectar automaticamente';
    el('output-name').textContent = snap.output.name;
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
      const title = fault.kind === 'bluetooth' ? 'O Bluetooth não está disponível' : fault.kind === 'output' ? 'Não foi possível iniciar a saída de controle virtual' : 'Problema na configuração';
      const reason = fault.kind === 'bluetooth' ? mac ? 'O macOS ainda não permitiu que o Pad2Go use o Bluetooth, ou o Bluetooth está desligado.' : 'Ligue o Bluetooth e confira se há um adaptador compatível com Bluetooth LE.' : fault.kind === 'output' ? snap.platform === 'Windows' ? 'Confira se o driver ViGEmBus está instalado.' : mac ? 'No macOS não há controle virtual: escolha “Automática” ou “Somente movimento” em Ajustes → Conexão → Saída.' : 'Confira o acesso a /dev/uinput.' : '';
      el('system-alert').innerHTML = `<strong>${title}</strong><span>${reason} <span class="technical">(${escape(fault.message)})</span></span><div class="banner-actions"><button type="button" class="push" data-retry>${snap.restarting ? 'Tentando…' : 'Tentar novamente'}</button><button type="button" class="link" data-help="${fault.kind === 'bluetooth' ? 'bluetooth' : 'output'}">Como resolver</button></div>`;
    }
    el('connect').disabled = Boolean(fault);
    const busy = snap.discovery.step >= 1 && snap.discovery.step < 4 && !snap.discovery.failure && !snap.discovery.foreign;
    el('sidebar-status').textContent = fault ? 'Ação necessária' : snap.restarting ? 'Reiniciando…' : busy ? 'Conectando…' : snap.players.length ? `${snap.players.length} ${snap.players.length === 1 ? 'jogador' : 'jogadores'}` : 'Procurando controles';
    renderDevices(); renderDiscovery(); icons();
  }

  // --- Input test -------------------------------------------------------------
  const buttonIds = { up: 0x0001, down: 0x0002, left: 0x0004, right: 0x0008, start: 0x0010, back: 0x0020, l: 0x0100, r: 0x0200, guide: 0x0400, a: 0x1000, b: 0x2000, x: 0x4000, y: 0x8000 };
  const physicalLabels = { MINUS: '−', PLUS: '+', HOME: 'Home', CAPT: 'Captura', C: 'C (Chat)', L_STK: 'L3', R_STK: 'R3', UP: '↑', DOWN: '↓', LEFT: '←', RIGHT: '→', SL_L: 'SL', SR_L: 'SR', SL_R: 'SL', SR_R: 'SR' };
  const physicalOrder = ['A', 'B', 'X', 'Y', 'UP', 'DOWN', 'LEFT', 'RIGHT', 'L', 'R', 'ZL', 'ZR', 'L_STK', 'R_STK', 'MINUS', 'PLUS', 'HOME', 'CAPT', 'C', 'SL_L', 'SR_L', 'SL_R', 'SR_R', 'GL', 'GR'];
  const number = (value, digits = 2) => value.toFixed(digits).replace('.', ',');
  function showInput(sample) {
    Object.entries(buttonIds).forEach(([id, bit]) => el(`pad-${id}`).classList.toggle('active', Boolean(sample.buttons & bit)));
    el('stick-left').parentElement.classList.toggle('active', Boolean(sample.buttons & 0x0040));
    el('stick-right').parentElement.classList.toggle('active', Boolean(sample.buttons & 0x0080));
    const pressed = (sample.pressed || []).slice().sort((a, b) => (physicalOrder.indexOf(a) + 99) % 99 - (physicalOrder.indexOf(b) + 99) % 99);
    el('pressed-values').value = pressed.length ? pressed.map(name => physicalLabels[name] || name.replace(/_/g, ' ')).join(' · ') : '—';
    el('stick-left').style.transform = `translate(${sample.lx * 14}px,${-sample.ly * 14}px)`;
    el('stick-right').style.transform = `translate(${sample.rx * 14}px,${-sample.ry * 14}px)`;
    el('left-values').value = `X ${number(sample.lx)} · Y ${number(sample.ly)}`;
    el('right-values').value = `X ${number(sample.rx)} · Y ${number(sample.ry)}`;
    [['l', sample.lt], ['r', sample.rt]].forEach(([side, raw]) => {
      const value = Math.round(raw / 255 * 100);
      el(`trigger-${side}`).value = value; el(`trigger-${side}-value`).value = `${value}%${sample.analog ? '' : ' · digital'}`;
    });
    el('gyro-values').value = sample.gyro.map(v => number(v, 1)).join(' · ');
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
  function openTest(player) {
    stopStream(); ui.testing = player; resetInput();
    el('test-title').textContent = `Teste de entrada · Jogador ${player}`;
    inputStream = new EventSource(`/api/players/${player}/input`);
    inputStream.addEventListener('input', event => showInput(JSON.parse(event.data)));
    inputStream.onerror = () => { if (ui.testing === player) el('input-state').textContent = 'Sem entradas: o controle desconectou?'; };
    el('test-dialog').showModal();
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
    output: () => snap.platform === 'macOS' ? 'O macOS não permite criar controles virtuais. Em Ajustes → Conexão → Saída, escolha “Automática” ou “Somente movimento” e reinicie as conexões; o movimento continua disponível para emuladores via DSU.' : snap.platform === 'Windows' ? 'Instale o <a href="https://github.com/nefarius/ViGEmBus" target="_blank" rel="noreferrer">ViGEmBus</a> e reinicie o Pad2Go. Se o jogo detectar entradas duplicadas, use o <a href="https://github.com/nefarius/HidHide" target="_blank" rel="noreferrer">HidHide</a> para ocultar o controle físico.' : 'Carregue o módulo com <code>sudo modprobe uinput</code>, confira se /dev/uinput existe e dê acesso ao seu usuário com uma regra udev.',
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

  document.addEventListener('click', async event => {
    const button = event.target.closest('button'); if (!button || button.disabled) return;
    if (button.dataset.view) setView(button.dataset.view);
    else if (button.id === 'connect') { ui.discoveryOpen = true; render(); }
    else if (button.id === 'show-howto') { ui.discoveryOpen = true; render(); el('howto').scrollIntoView({ block: 'nearest', behavior: 'smooth' }); }
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
    else if (button.id === 'rumble') {
      try { await api(`/api/players/${ui.testing}/rumble`, { method: 'POST' }); inputNote('Vibração enviada ao controle.'); } catch (error) { inputNote(`Não foi possível vibrar: ${error.message}`); }
    }
    else if (button.id === 'copy-address') { const address = snap.dsu.address; try { await navigator.clipboard.writeText(address); el('copy-result').textContent = `Copiado: ${address}`; } catch { el('copy-result').textContent = `Copie: ${address}`; } }
    else if (button.dataset.help) showHelp(button);
    else if (button.id === 'check-system') { await diagnose(); announce('Verificação concluída.'); }
  });
  document.addEventListener('change', event => {
    const node = event.target;
    if (node.id === 'connection-notifications') { ui.notifications = node.checked; persistPrefs(); if (!node.checked) el('connection-toasts').replaceChildren(); return; }
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
  icons(); render(); connectEvents();
})();
