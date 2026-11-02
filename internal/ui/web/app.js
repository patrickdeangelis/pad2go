/* Pad2Go interface (Preact + htm, vendored, no build step). State comes from
   the local pad2go process: GET /api/state, live updates over /api/events,
   input tests over /api/players/{n}/input. Settings are saved to config.yaml
   by the server. */
import { html, render, useState, useEffect, useLayoutEffect, useRef } from './vendor/htm-preact.module.js';
import { Mapping } from './mapping.js';
import { TestDialog } from './test-dialog.js';

const prefsKey = 'pad2go-ui-v2';
let initialNotifications = true;
try {
  const saved = JSON.parse(localStorage.getItem(prefsKey));
  if (saved) initialNotifications = saved.notifications !== false;
} catch { /* Storage is optional. */ }

// Behave like a desktop app: no page context menu or text selection
// outside editable fields.
document.addEventListener('contextmenu', event => {
  if (!event.target.closest('input, select')) event.preventDefault();
});

const paths = {
  gamepad: html`<path d="M6 8h12c2 0 3 2 3 4l1 5c.5 3-2 4-4 2l-2-2H8l-2 2c-2 2-4.5 1-4-2l1-5c0-2 1-4 3-4Z"/><path d="M6 11v5m-2.5-2.5h5m7.5-1h.01m2 2h.01"/>`,
  orbit: html`<circle cx="12" cy="12" r="3"/><ellipse cx="12" cy="12" rx="10" ry="5" transform="rotate(-35 12 12)"/>`,
  sliders: html`<path d="M4 7h9m4 0h3M4 17h3m4 0h9"/><circle cx="15" cy="7" r="2"/><circle cx="9" cy="17" r="2"/>`,
  gear: html`<circle cx="12" cy="12" r="3"/><path d="M12 2v3m0 14v3M4.9 4.9l2.1 2.1m10 10 2.1 2.1M2 12h3m14 0h3M4.9 19.1 7 17M17 7l2.1-2.1"/>`,
  bluetooth: html`<path d="m7 7 10 10-5 4V3l5 4L7 17"/>`,
  monitor: html`<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M12 17v4m-4 0h8"/>`,
  chevron: html`<path d="m9 6 6 6-6 6"/>`,
  info: html`<circle cx="12" cy="12" r="9.5"/><path d="M12 11v6m0-9.5h.01"/>`,
  check: html`<path d="m5 12 4 4L19 6"/>`,
  x: html`<path d="m6 6 12 12M6 18 18 6"/>`,
  battery: html`<rect x="2" y="7" width="17" height="10" rx="2.5"/><path d="M22 10.5v3M5 10v4m3-4v4m3-4v4"/>`,
  'battery-medium': html`<rect x="2" y="7" width="17" height="10" rx="2.5"/><path d="M22 10.5v3M5 10v4m3-4v4"/>`,
  'battery-low': html`<rect x="2" y="7" width="17" height="10" rx="2.5"/><path d="M22 10.5v3M5 10v4"/>`,
  warn: html`<path d="M12 3 2 20h20L12 3Z"/><path d="M12 10v4m0 3h.01"/>`,
};

function Icon({ name }) {
  return html`<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || paths.check}</svg>`;
}

let snap = null;
let draft = null;
let saveTimer = null;
let saving = false;
let feedback = { message: '', error: false };
const listeners = new Set();

function notify() {
  listeners.forEach(callback => callback());
}

function announce(message, error = false) {
  feedback = { message, error };
  notify();
}

async function api(path, { method = 'GET', body } = {}) {
  const response = await fetch(path, {
    method,
    headers: { 'X-Pad2go': '1', ...(body ? { 'Content-Type': 'application/json' } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!response.ok) {
    let message = response.statusText;
    try { message = (await response.json()).error || message; } catch { /* Not JSON. */ }
    throw new Error(message);
  }
  return response.status === 200 ? response.json() : null;
}

function save(delay = 0) {
  clearTimeout(saveTimer);
  saving = true;
  saveTimer = setTimeout(async () => {
    try {
      snap = await api('/api/config', { method: 'PUT', body: draft });
      draft = structuredClone(snap.config);
      feedback = { message: '', error: false };
    } catch (error) {
      feedback = { message: `Não foi possível salvar: ${error.message}`, error: true };
      draft = structuredClone(snap.config);
    } finally {
      saving = false;
    }
    notify();
  }, delay);
}

window.pad2go = {
  state: () => ({ snap, draft }),
  update(mutator) { if (!draft) return; mutator(draft); save(); notify(); },
  onChange(callback) { listeners.add(callback); return () => listeners.delete(callback); },
  api: (path, options) => api(path, options),
  openTest: null, // set by test-dialog.js
  closeTest: null,
};

// --- Players & Controller Art -----------------------------------------------
const controllerAsset = kind => ({ ds: 'dualsense', left: 'joycon-l', right: 'joycon-r', pro: 'pro', gc: 'gamecube' }[kind] || 'pro');
const holdMode = member => draft?.holds?.[member?.addr] || draft?.hold || snap?.config?.holds?.[member?.addr] || snap?.config?.hold || 'Vertical';

function ControllerArt({ members }) {
  const kinds = members.length === 2 ? ['left', 'right'] : [members[0]?.kind || 'pro'];
  const horizontal = members.length === 1 && ['left', 'right'].includes(kinds[0]) && holdMode(members[0]) === 'Horizontal';
  return html`<div class=${`controller-mini${members.length === 2 ? ' pair' : ''}`}>
    ${kinds.map(kind => html`<img class=${horizontal ? 'sideways' : ''} src=${`assets/controllers/${controllerAsset(kind)}.svg`} alt="" aria-hidden="true" />`)}
  </div>`;
}

const ledPatterns = [0, 1, 3, 7, 15, 9, 5, 13, 6]; // protocol.LEDPattern
function Leds({ player, kind }) {
  const dualSense = kind === 'ds';
  const pattern = dualSense ? [0, 0x04, 0x0a, 0x15, 0x1b, 0x1f, 0x1f, 0x1f, 0x1f][player] : ledPatterns[player];
  const bits = dualSense ? [0, 1, 2, 3, 4] : [0, 1, 2, 3];
  return html`<span class=${`leds${dualSense ? ' dualsense-leds' : ''}`} role="img" aria-label=${`LEDs do jogador ${player}`}>
    ${bits.map(bit => html`<span class=${pattern & (1 << bit) ? 'lit' : ''}></span>`)}
  </span>`;
}

const lowBattery = member => member.battery === 'low';
const batteryLabels = { high: 'Alta', medium: 'Média', low: 'Baixa' };
const batteryIcons = { high: 'battery', medium: 'battery-medium', low: 'battery-low' };
const batteryText = m => m.batteryPercent != null
  ? `${m.batteryStatus === 'full' ? '' : '≈ '}${m.batteryPercent}%${m.batteryStatus === 'charging' ? ' · Carregando' : m.batteryStatus === 'full' ? ' · Carregada' : ''}`
  : m.batteryStatus === 'error' ? 'Falha na bateria' : (batteryLabels[m.battery] || 'Desconhecida');
const volts = v => `${v.toFixed(2).replace('.', ',')} V`;
const reportStatus = d => !d ? '' : d.lastValidMillis < 0 ? 'Aguardando relatórios' : d.lastValidMillis > 2000 ? 'Sem relatórios recentes' : d.invalid > 0 ? 'Relatórios inválidos detectados' : 'Recebendo relatórios';
const diagnosticText = d => `Relatórios: ${d.valid} válidos · ${d.invalid} inválidos · ${d.lastValidMillis < 0 ? 'aguardando entrada' : `último há ${(d.lastValidMillis / 1000).toFixed(1)} s`} · sensores ${d.calibrated ? 'calibrados' : 'sem calibração'}`;

// --- Help & Remedies --------------------------------------------------------
function HelpParagraph({ kind, snap: currentSnap }) {
  if (kind === 'bluetooth') {
    const text = currentSnap?.platform === 'macOS'
      ? 'Na primeira execução o macOS pergunta se o Pad2Go pode usar o Bluetooth: clique em Permitir. Se você recusou, ative o Pad2Go em Ajustes do Sistema → Privacidade e Segurança → Bluetooth. Confira se o Bluetooth está ligado e clique em “Tentar novamente”.'
      : 'Ligue o Bluetooth nas configurações do computador e confirme que há um adaptador compatível com Bluetooth LE. Depois, clique em “Tentar novamente”.';
    return html`<p class="help" data-for="bluetooth">${text}</p>`;
  }
  if (kind === 'mac') {
    return html`<p class="help" data-for="mac">Informe o endereço Bluetooth do computador em Ajustes → Conexão. Sem ele a conexão funciona, mas o pareamento para reconectar com um toque não é salvo.</p>`;
  }
  if (kind === 'output') {
    if (currentSnap?.platform === 'macOS') {
      return html`<p class="help" data-for="output">
        O macOS não permite criar controles virtuais. Em Controles → Conexão → Saída padrão, escolha “Automática” ou “Somente movimento”; o movimento continua disponível para emuladores via DSU.
        ${currentSnap?.fault?.kind === 'output' && html`<span class="technical">Detalhe técnico: ${currentSnap.fault.message}</span>`}
      </p>`;
    }
    const linux = currentSnap?.platform === 'Linux';
    const available = currentSnap?.output?.available || {};
    const def = currentSnap?.output?.default;
    const xbox = (def === 'auto' || def === 'xbox') && available.xbox === false;
    const hid = (def === 'auto' && available.dualsense === false) || ((def === 'dualsense' || def === 'switch') && available[def] === false);
    const vigem = linux
      ? html`Xbox 360: carregue o módulo com <code>sudo modprobe uinput</code>, confira se /dev/uinput existe e dê acesso ao seu usuário com uma regra udev.`
      : html`Xbox 360: o instalador do Pad2Go já instala o ViGEmBus e traz um build com suporte a ele. Se você usou o pad2go.exe avulso, instale o <a href="https://github.com/nefarius/ViGEmBus/releases/latest" target="_blank" rel="noreferrer">ViGEmBus 1.22</a> e use o build do instalador.`;
    const winuhid = linux
      ? html`DualSense/Switch Pro: confira se /dev/uhid existe e se o seu usuário tem acesso a ele.`
      : html`DualSense/Switch Pro: instale o driver <a href="https://github.com/cgutman/WinUHid" target="_blank" rel="noreferrer">WinUHid</a>. Em emuladores de Switch, escolha o controle como Pro Controller no driver SDL2.`;
    const parts = [xbox && vigem, hid && winuhid].filter(Boolean);
    if (!parts.length) parts.push(def === 'dualsense' || def === 'switch' ? winuhid : vigem);
    parts.push(html`Depois, clique em “Tentar novamente”. Se o jogo detectar entradas duplicadas, use o <a href="https://github.com/nefarius/HidHide" target="_blank" rel="noreferrer">HidHide</a> para ocultar o controle físico.`);
    return html`<p class="help" data-for="output">
      ${parts.map(part => html`<span class="help-line">${part}</span>`)}
      ${currentSnap?.fault?.kind === 'output' && html`<span class="technical">Detalhe técnico: ${currentSnap.fault.message}</span>`}
    </p>`;
  }
  return null;
}

// --- Validated Inputs -------------------------------------------------------
function validateField(key, rawValue, isDeadzone = false) {
  if (isDeadzone) {
    return rawValue === '' || !Number.isFinite(Number(rawValue)) || Number(rawValue) < 0 || Number(rawValue) > 50
      ? 'Use um valor entre 0 e 50.'
      : '';
  }
  if (key === 'host' && !rawValue.trim()) return 'Informe o endereço do servidor.';
  if (key === 'port' && (!rawValue || !Number.isInteger(Number(rawValue)) || Number(rawValue) < 1 || Number(rawValue) > 65535)) return 'Use uma porta entre 1 e 65535.';
  if (key === 'hostMac' && rawValue.trim() && !/^(?:[a-f\d]{12}|(?:[a-f\d]{2}:){5}[a-f\d]{2}|(?:[a-f\d]{2}-){5}[a-f\d]{2})$/i.test(rawValue.trim())) return 'Use AA:BB:CC:DD:EE:FF.';
  if (key === 'autoDisconnectMinutes' && (rawValue === '' || !Number.isInteger(Number(rawValue)) || Number(rawValue) < 0 || Number(rawValue) > 120)) return 'Use um valor inteiro entre 0 e 120 minutos.';
  return '';
}

function ValidatedInput({ value, error, onCommit, ...props }) {
  const ref = useRef(null);
  const dirty = useRef(false);
  useLayoutEffect(() => {
    if (ref.current && !dirty.current && !error && document.activeElement !== ref.current) {
      ref.current.value = value ?? '';
    }
  }, [value, error]);
  return html`<input ...${props} ref=${ref} defaultValue=${value ?? ''} aria-invalid=${String(Boolean(error))}
    onInput=${() => { dirty.current = true; }}
    onBlur=${() => { dirty.current = false; }}
    onChange=${event => { dirty.current = false; onCommit(event.currentTarget.value); }} />`;
}

// --- Panels -----------------------------------------------------------------
const stepNames = ['Procurando', 'Encontrado', 'Conectando', 'Pareando', 'Pronto'];

function Discovery({ snap: currentSnap, open, kind, setKind, onClose, onAcceptForeign, onOpenBluetooth, discoveryRef }) {
  const d = currentSnap?.discovery || { step: -1, text: '', bonded: false, failure: false, foreign: false };
  const blocking = currentSnap?.fault && currentSnap.fault.kind !== 'output';
  const idleSpinner = Boolean(blocking) || d.failure || d.foreign || d.step === 4 || d.step < 0;
  const statusText = blocking
    ? 'Busca pausada: resolva o problema indicado acima.'
    : d.text || 'A busca é automática. Não é preciso escolher o controle.';
  const reconnectText = currentSnap?.platform === 'macOS' && !currentSnap?.config?.hostMac
    ? 'No macOS o Pad2Go não lê o endereço Bluetooth do computador, então o pareamento não fica salvo: segure SYNC a cada conexão (ou informe o endereço em Ajustes → Conexão).'
    : 'Depois do primeiro pareamento, basta apertar qualquer botão para reconectar.';

  return html`<div id="discovery" ref=${discoveryRef} class="group discovery" hidden=${!open} aria-labelledby="discovery-choice">
    <div class="row">
      <div class="row-label"><span id="discovery-choice">Qual controle você quer conectar?</span><small>Escolha o tipo para ver os passos corretos.</small></div>
      <div class="row-control">
        <button type="button" class="push" data-connect-kind="nintendo" onClick=${() => setKind('nintendo')}>Nintendo</button>
        <button type="button" class="push" data-connect-kind="dualsense" onClick=${() => setKind('dualsense')}>DualSense</button>
        <button id="hide-discovery" type="button" class="push" onClick=${onClose}>Fechar</button>
      </div>
    </div>
    <div id="nintendo-discovery" hidden=${kind !== 'nintendo'}>
      <div class="row">
        <span class=${`spinner${idleSpinner ? ' idle' : ''}`} id="discovery-spinner" aria-hidden="true"></span>
        <div class="row-label"><span id="discovery-title">Conexão automática</span><small id="discovery-status" role="status" aria-live="polite">${statusText}</small></div>
      </div>
      <div class="row">
        <ol id="discovery-steps" class="steps" aria-label="Etapas de conexão">
          ${stepNames.map((name, index) => {
            const cls = index < d.step || (d.step === 4 && index === 4) ? 'done' : index === d.step ? 'current' : '';
            return html`<li key=${name} class=${cls} aria-current=${index === d.step ? 'step' : undefined}>${name}</li>`;
          })}
        </ol>
      </div>
      <div class=${`row howto${d.bonded ? ' attention' : ''}`} id="howto">
        <div class="row-label">
          <span>Como conectar</span>
          <ol>
            <li>Desligue o Switch 2 ou ative o modo avião. Ligado e por perto, ele chama os controles de volta.</li>
            <li>Tire o cabo USB do controle. Ligado por cabo, ele usa o USB e não fica no Bluetooth.</li>
            <li>Segure o botão <strong>SYNC</strong> até as luzes correrem de um lado para o outro.
              <ul>
                <li><strong>Pro Controller 2 e controle GameCube:</strong> botão na parte de cima, perto da entrada USB-C.</li>
                <li><strong>Joy-Con 2:</strong> botão redondo pequeno na face interna (o lado que encaixa no console), entre SL e SR. Faça em cada um.</li>
              </ul>
            </li>
            <li>O Pad2Go conecta sozinho e cada controle vira um jogador. Com “Unir Joy-Cons” ligado, um Joy-Con esquerdo e um direito viram um só jogador.</li>
            <li id="howto-reconnect">${reconnectText}</li>
          </ol>
          <small>Para voltar a usar no Switch 2, conecte o controle ao console pelo cabo ou encaixe os Joy-Cons.</small>
        </div>
      </div>
      <div class="row" id="foreign-action" hidden=${!d.foreign || Boolean(currentSnap?.config?.foreign)}>
        <div class="row-label"><span>Controle pareado com outro PC</span><small>A preferência será salva e as conexões serão reiniciadas.</small></div>
        <div class="row-control"><button id="accept-foreign" type="button" class="push" onClick=${onAcceptForeign}>Aceitar mesmo assim</button></div>
      </div>
    </div>
    <div id="dualsense-discovery" class="row howto" hidden=${kind !== 'dualsense'}>
      <div class="row-label">
        <span>Conectar DualSense</span>
        <ol>
          <li>Desconecte o cabo USB e desligue o controle, se estiver ligado.</li>
          <li>Segure <strong>PS + Criar</strong> até as luzes começarem a piscar.</li>
          <li>No Bluetooth do Windows, escolha <strong>Adicionar dispositivo → Bluetooth → Wireless Controller</strong> e confirme o pareamento.</li>
          <li>Volte ao Pad2Go. O controle conectado aparecerá na lista de jogadores.</li>
        </ol>
        <small>Um DualSense já pareado pode ser ligado com o botão PS. Por USB, conecte o cabo diretamente ao PC.</small>
      </div>
      <div class="row-control"><button id="open-bluetooth-settings" type="button" class="push" hidden=${currentSnap?.platform !== 'Windows'} onClick=${onOpenBluetooth}>Abrir Bluetooth do Windows</button></div>
    </div>
  </div>`;
}

function DeviceList({ snap: currentSnap, draft: currentDraft, detail, setDetail, onSetPlayerOutput, onDisconnect, onUpdateHold }) {
  const players = currentSnap?.players || [];
  const mac = currentSnap?.platform === 'macOS';
  if (!players.length) {
    return html`<div id="device-list" class="group">
      <div class="empty"><strong>Nenhum controle</strong>Segure SYNC em um controle novo ou pressione um botão em um já pareado.</div>
    </div>`;
  }
  return html`<div id="device-list" class="group">
    ${players.map(slot => {
      const pair = slot.members.length === 2;
      const low = slot.members.some(lowBattery);
      const warning = slot.members.find(m => m.diagnostics && (m.diagnostics.lastValidMillis > 2000 || m.diagnostics.invalid > 0 || !m.diagnostics.calibrated));
      const status = low
        ? 'Bateria baixa'
        : warning
          ? (warning.diagnostics.lastValidMillis > 2000 || warning.diagnostics.invalid > 0 ? reportStatus(warning.diagnostics) : 'Calibração indisponível')
          : 'Conectado';
      const single = slot.members.length === 1 && ['left', 'right'].includes(slot.members[0].kind);
      const open = detail === slot.player;
      const defaultLabel = slot.defaultOutput ? `${slot.defaultOutput} (padrão)` : 'Padrão';
      const outputPill = slot.outputOverrideKind && currentSnap?.output?.available && currentSnap.output.available[slot.outputOverrideKind] === false
        ? `${slot.outputOverride} indisponível`
        : slot.output;
      const holdAddr = slot.members[0]?.addr;
      const currentHold = currentDraft?.holds?.[holdAddr] || currentDraft?.hold || 'Vertical';

      return html`<div key=${slot.player} class="row device-row" data-player=${slot.player}>
          <div class="device-icon" aria-hidden="true"><div class="art"><${ControllerArt} members=${slot.members} /></div></div>
          <div class="row-label">
            <span>Jogador ${slot.player} · ${slot.name}</span>
            <small class="device-meta">
              <span class=${`status ${low || warning ? 'warning' : 'on'}`}>${status}</span>
              <span class="output-pill">${outputPill}${slot.outputOverride ? ' · manual' : ''}</span>
              ${slot.outputIssue ? html`<span class="output-issue">${slot.outputIssue}</span>` : null}
              ${slot.members.filter(m => m.battery || m.batteryStatus).map(m => html`
                <span class=${`battery${lowBattery(m) || m.batteryStatus === 'error' ? ' low' : ''}`} title=${m.batteryPercent != null && m.batteryStatus !== 'full' ? 'Estimativa em intervalos de 10%' : 'Bateria'}>
                  <${Icon} name=${batteryIcons[m.battery] || 'warn'} />${pair ? (m.kind === 'left' ? 'L ' : 'R ') : ''}${batteryText(m)}
                </span>`)}
              <${Leds} player=${slot.player} kind=${slot.members[0]?.kind} />
            </small>
          </div>
          <div class="row-control">
            <select class="player-output" data-player-output=${slot.player} title=${`Saída: ${slot.output}`} aria-label=${`Saída do jogador ${slot.player}`}
              value=${slot.outputOverrideKind || 'auto'} onChange=${event => onSetPlayerOutput(slot.player, event.currentTarget.value)}>
              <option value="auto">${defaultLabel}</option>
              <option value="xbox" disabled=${mac}>Xbox 360</option>
              <option value="dualsense" disabled=${mac}>DualSense</option>
              <option value="switch" disabled=${mac}>Switch Pro</option>
              <option value="none">Somente movimento</option>
            </select>
            <button type="button" class="push" data-test=${slot.player} onClick=${() => window.pad2go.openTest?.(slot.player)}>Testar</button>
            <button type="button" class="info-button" data-detail=${slot.player} aria-expanded=${String(open)} aria-label=${`Detalhes do jogador ${slot.player}`} title="Detalhes"
              onClick=${() => setDetail(open ? null : slot.player)}><${Icon} name="info" /></button>
          </div>
        </div>
        ${open && html`<div key=${`detail-${slot.player}`} class="device-details">
          <span>${slot.members.map(m => `${m.transport || (m.kind === 'ds' ? 'USB' : 'Bluetooth LE')} · ${m.addr}`).join(' · ')}</span>
          ${slot.members.map((m, i) => m.diagnostics ? html`<span key=${i} data-report-diagnostics=${`${slot.player}:${i}`}>${diagnosticText(m.diagnostics)}</span>` : null)}
          ${slot.members.some(m => m.battery && m.volts > 0) && html`<span>Bateria estimada pela tensão: ${slot.members.filter(m => m.battery && m.volts > 0).map(m => `${pair ? (m.kind === 'left' ? 'L ' : 'R ') : ''}${volts(m.volts)}`).join(' · ')}</span>`}
          ${single && html`<label class="detail-line">Posição deste Joy-Con
            <select data-device-hold=${holdAddr} value=${currentHold} onChange=${event => onUpdateHold(holdAddr, event.currentTarget.value)}>
              <option value="Vertical">Vertical</option>
              <option value="Horizontal">Horizontal</option>
            </select>
          </label>`}
          <button type="button" class="danger-link" data-disconnect=${slot.player} onClick=${() => onDisconnect(slot.player)}>Desconectar ${pair ? 'par' : 'controle'}</button>
        </div>`}`;
    })}
  </div>`;
}

// --- Connection Toasts ------------------------------------------------------
let toastSeq = 0;
let addToast = null;
let clearToasts = null;

function Toasts() {
  const [items, setItems] = useState([]);
  useEffect(() => {
    addToast = ({ name, connected, player }) => {
      const id = ++toastSeq;
      setItems(current => [...current.slice(-2), { id, name, connected, player }]);
      setTimeout(() => setItems(current => current.filter(item => item.id !== id)), 5000);
    };
    clearToasts = () => setItems([]);
  }, []);
  return html`${items.map(item => html`<section key=${item.id} class="toast">
    <img src="assets/pad2go-icon.png" alt="" />
    <div><strong>${`${item.name} ${item.connected ? 'conectado' : 'desconectado'}`}</strong><span>${`Jogador ${item.player}`}</span></div>
    <button type="button" aria-label="Fechar aviso" onClick=${() => setItems(current => current.filter(t => t.id !== item.id))}><${Icon} name="x" /></button>
  </section>`)}`;
}

// --- Main Application -------------------------------------------------------
const tabDefs = [
  ['controllers', 'gamepad', 'Controles'],
  ['motion', 'orbit', 'Movimento'],
  ['mapping', 'sliders', 'Mapeamento'],
  ['settings', 'gear', 'Ajustes'],
];
const titles = { controllers: 'Controles', motion: 'Movimento', mapping: 'Mapeamento', settings: 'Ajustes' };

function App() {
  const [, setTick] = useState(0);
  const [view, setView] = useState('controllers');
  const [scrolled, setScrolled] = useState(false);
  const [offline, setOffline] = useState(false);
  const [detail, setDetail] = useState(null);
  const [discoveryOpen, setDiscoveryOpen] = useState(false);
  const [discoveryKind, setDiscoveryKind] = useState(null);
  const [notifications, setNotifications] = useState(initialNotifications);
  const [errors, setErrors] = useState({});
  const [copyResult, setCopyResult] = useState('');
  const [diagnostics, setDiagnostics] = useState([]);
  const [bannerHelp, setBannerHelp] = useState(false);
  const [diagHelp, setDiagHelp] = useState({});

  const mainRef = useRef(null);
  const tabRefs = useRef({});
  const connectBtnRef = useRef(null);
  const discoveryRef = useRef(null);
  const defaultOutputRef = useRef(null);
  const bondedShown = useRef(false);
  const lastStep = useRef(undefined);
  const closeTimer = useRef(null);
  const notificationsRef = useRef(notifications);
  notificationsRef.current = notifications;

  useEffect(() => window.pad2go.onChange(() => setTick(t => t + 1)), []);

  async function diagnose(announceDone = false) {
    try {
      const rows = await api('/api/diagnostics');
      setDiagnostics(rows || []);
      setDiagHelp({});
      if (announceDone) announce('Verificação concluída.');
    } catch (error) {
      announce(`Não foi possível verificar: ${error.message}`, true);
    }
  }

  async function restart() {
    try {
      await api('/api/restart', { method: 'POST' });
      window.pad2go.closeTest?.();
      announce('Conexões reiniciadas. Pressione um botão nos controles para reconectar.');
    } catch (error) {
      announce(`Não foi possível reiniciar: ${error.message}`, true);
    }
  }

  async function setDefaultOutput(value, apply) {
    try {
      snap = await api('/api/output', { method: 'POST', body: { backend: value, apply } });
      notify();
      announce(apply ? 'Saída padrão aplicada aos players atuais.' : 'Saída padrão atualizada.');
    } catch (error) {
      announce(`Não foi possível mudar a saída: ${error.message}`, true);
      notify();
    }
  }

  async function setPlayerOutput(player, value) {
    try {
      snap = await api(`/api/players/${player}/output`, { method: 'POST', body: { output: value } });
      notify();
      announce(`Saída do jogador ${player} atualizada.`);
    } catch (error) {
      announce(`Não foi possível mudar a saída: ${error.message}`, true);
      notify();
    }
  }

  // Live SSE events + 2s DualSense diagnostics poll
  useEffect(() => {
    const events = new EventSource('/api/events');
    events.addEventListener('state', event => {
      setOffline(false);
      const nextSnap = JSON.parse(event.data);
      const prevFaultKind = snap?.fault?.kind;
      snap = nextSnap;
      if (!draft || !saving) draft = structuredClone(snap.config);
      if (prevFaultKind !== snap.fault?.kind) setBannerHelp(false);
      setDetail(current => (current !== null && !snap.players.some(p => p.player === current) ? null : current));

      const d = snap.discovery;
      if (d.bonded && !bondedShown.current) {
        setDiscoveryOpen(open => {
          if (!open) setDiscoveryKind('nintendo');
          return true;
        });
      }
      bondedShown.current = Boolean(d.bonded);

      if (d.step === 4 && lastStep.current !== 4) {
        clearTimeout(closeTimer.current);
        closeTimer.current = setTimeout(() => {
          const waitingPartner = snap?.config?.combine && snap?.players?.some(p => p.members.length === 1 && ['left', 'right'].includes(p.members[0].kind));
          if (snap?.discovery?.step === 4 && !waitingPartner) {
            setDiscoveryOpen(false);
          }
        }, 1500);
      }
      lastStep.current = d.step;
      notify();
    });
    events.addEventListener('toast', event => {
      if (notificationsRef.current) addToast?.(JSON.parse(event.data));
    });
    events.onerror = () => setOffline(true);

    const interval = setInterval(async () => {
      if (!snap?.players?.some(p => p.members.some(m => m.diagnostics))) return;
      try {
        const current = await api('/api/state');
        if (snap) { snap = current; notify(); }
      } catch { /* EventSource reports disconnects. */ }
    }, 2000);

    return () => {
      events.close();
      clearInterval(interval);
      clearTimeout(closeTimer.current);
    };
  }, []);

  function selectView(nextView, focusTab = false) {
    setView(nextView);
    announce('');
    if (mainRef.current) mainRef.current.scrollTop = 0;
    if (nextView === 'settings') diagnose(false);
    if (focusTab) requestAnimationFrame(() => tabRefs.current[nextView]?.focus());
  }

  function onTabKeyDown(event) {
    if (!['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
    const index = tabDefs.findIndex(([id]) => id === view);
    if (index < 0) return;
    event.preventDefault();
    const nextIndex = event.key === 'Home' ? 0 : event.key === 'End' ? tabDefs.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + tabDefs.length) % tabDefs.length;
    selectView(tabDefs[nextIndex][0], true);
  }

  function commitConfigField(key, rawValue, numeric = false) {
    if (!draft) return;
    const message = validateField(key, rawValue, false);
    setErrors(current => ({ ...current, [key]: message }));
    if (message) {
      announce(message, true);
      return;
    }
    draft[key] = numeric ? Number(rawValue) : rawValue.trim();
    save();
    notify();
  }

  function commitDeadzone(kind, rawValue) {
    if (!draft) return;
    const errorKey = `dz-${kind}`;
    const message = validateField(kind, rawValue, true);
    setErrors(current => ({ ...current, [errorKey]: message }));
    if (message) {
      announce(message, true);
      return;
    }
    draft.deadzones[kind] = Number(rawValue);
    save();
    notify();
  }

  function updateCheckboxOrSelect(key, value) {
    if (!draft) return;
    draft[key] = value;
    save();
    notify();
  }

  function updateRange(key, value) {
    if (!draft) return;
    draft[key] = Number(value);
    save(400);
    notify();
  }

  function openDiscovery() {
    setDiscoveryOpen(true);
    setDiscoveryKind(null);
    requestAnimationFrame(() => discoveryRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' }));
  }

  function closeDiscovery() {
    setDiscoveryOpen(false);
    requestAnimationFrame(() => connectBtnRef.current?.focus());
  }

  async function acceptForeign() {
    if (!draft) return;
    draft.foreign = true;
    clearTimeout(saveTimer);
    try {
      snap = await api('/api/config', { method: 'PUT', body: draft });
      draft = structuredClone(snap.config);
      await restart();
    } catch (error) {
      announce(`Não foi possível salvar: ${error.message}`, true);
    }
  }

  async function openBluetoothSettings() {
    try {
      await api('/api/bluetooth/settings', { method: 'POST' });
    } catch (error) {
      announce(`Não foi possível abrir as configurações: ${error.message}`, true);
    }
  }

  async function disconnectPlayer(player) {
    try {
      await api(`/api/players/${player}/disconnect`, { method: 'POST' });
      setDetail(null);
      announce(`Jogador ${player} desconectado. Os demais mantêm seus números.`);
    } catch (error) {
      announce(`Não foi possível desconectar: ${error.message}`, true);
    }
  }

  function updateDeviceHold(addr, value) {
    if (!draft) return;
    draft.holds = { ...draft.holds, [addr]: value };
    save();
    notify();
  }

  async function copyAddress() {
    const address = snap?.dsu?.address || '';
    try {
      await navigator.clipboard.writeText(address);
      setCopyResult(`Copiado: ${address}`);
    } catch {
      setCopyResult(`Copie: ${address}`);
    }
  }

  function toggleNotifications(checked) {
    setNotifications(checked);
    try { localStorage.setItem(prefsKey, JSON.stringify({ notifications: checked })); } catch { /* Optional. */ }
    if (!checked) clearToasts?.();
  }

  const players = snap?.players || [];
  const mac = snap?.platform === 'macOS';
  const dsu = snap?.dsu || { running: false, error: '', enabled: false, address: '', clients: 0 };
  const fault = snap?.fault;
  const busy = snap && snap.discovery.step >= 1 && snap.discovery.step < 4 && !snap.discovery.failure && !snap.discovery.foreign;
  const sidebarStatus = offline
    ? 'Sem conexão'
    : !snap
      ? 'Conectando…'
      : fault
        ? 'Ação necessária'
        : snap.restarting
          ? 'Reiniciando…'
          : busy
            ? 'Conectando…'
            : players.length
              ? `${players.length} ${players.length === 1 ? 'jogador' : 'jogadores'}`
              : 'Procurando controles';

  const subtitle = !snap
    ? 'Prontos para o próximo jogo.'
    : players.length
      ? (snap.output.motionOnly ? 'Enviando movimento aos emuladores.' : 'Prontos para o próximo jogo.')
      : 'Nenhum controle conectado.';

  const defaultOutputValue = snap ? (['vigem', 'uinput'].includes(snap.output.default) ? 'xbox' : snap.output.default) : 'auto';
  const bluetoothState = snap ? ({ on: 'Ativo · busca automática', off: 'Indisponível', idle: 'Não iniciado' }[snap.bluetooth] || '') : 'Iniciando…';
  const motionStateText = dsu.running ? 'Ativo' : dsu.error ? 'Falha ao iniciar' : dsu.enabled ? 'Aguardando reinício' : 'Desativado';
  const motionStateClass = `status ${dsu.running ? 'on' : dsu.error ? 'error' : dsu.enabled ? 'warning' : ''}`;
  const motionAddress = dsu.error ? `${dsu.address} · ${dsu.error}` : dsu.address;

  const outputFault = fault?.kind === 'output';
  const faultTitle = fault?.kind === 'bluetooth'
    ? 'O Bluetooth não está disponível'
    : outputFault
      ? 'Sem saída virtual: os jogos não veem os controles'
      : 'Problema na configuração';
  const faultReason = fault?.kind === 'bluetooth'
    ? (mac ? 'O macOS ainda não permitiu que o Pad2Go use o Bluetooth, ou o Bluetooth está desligado.' : 'Ligue o Bluetooth e confira se há um adaptador compatível com Bluetooth LE.')
    : '';
  const outputReasons = Array.isArray(fault?.reasons) && fault.reasons.length
    ? fault.reasons
    : [snap?.platform === 'Windows' ? 'Os controles conectam e podem ser testados, mas falta uma saída virtual.' : mac ? 'No macOS não há controle virtual: escolha “Automática” ou “Somente movimento”.' : 'Confira o acesso a /dev/uinput.'];

  const sensitivity = draft?.sensitivity ?? 1;
  const vibration = draft?.vibration ?? 5;

  return html`
    <nav class="sidebar" aria-label="Pad2Go">
      <div class="sidebar-header">
        <img src="assets/pad2go-icon.png" width="32" height="32" alt="" />
        <div><strong>Pad2Go</strong><span id="sidebar-status">${sidebarStatus}</span></div>
      </div>
      <div class="sidebar-items" role="tablist" aria-orientation="vertical" aria-label="Seções" onKeyDown=${onTabKeyDown}>
        ${tabDefs.map(([id, iconName, label]) => html`
          <button key=${id} id=${`tab-${id}`} ref=${node => { tabRefs.current[id] = node; }} type="button" role="tab"
            aria-selected=${String(view === id)} aria-controls=${`panel-${id}`} data-view=${id} tabIndex=${view === id ? 0 : -1}
            onClick=${() => selectView(id)}>
            <${Icon} name=${iconName} />
            <span>${label}</span>
            ${id === 'controllers' && html`<span id="player-badge" class="badge" hidden=${!players.length}>${players.length || ''}</span>`}
          </button>`)}
      </div>
      <div class="sidebar-footer" id="version">${snap ? `${snap.platform} · pad2go ${snap.version}` : ''}</div>
    </nav>

    <main id="main-content" ref=${mainRef} class="content" tabIndex="-1" onScroll=${event => setScrolled(event.currentTarget.scrollTop > 2)}>
      <div id="toolbar" class=${`toolbar${scrolled ? ' scrolled' : ''}`}><h1 id="toolbar-title">${titles[view]}</h1></div>
      <div class="content-inner">
        <div id="offline" class="banner error" role="alert" hidden=${!offline}>
          <strong>Sem conexão com o Pad2Go.</strong><span>O programa foi encerrado ou reiniciado. Tentando reconectar…</span>
        </div>
        <div id="pending" class="banner" hidden=${!snap?.pending && !snap?.restarting}>
          <div><strong>Alterações salvas</strong><span>Valem após reiniciar as conexões.</span></div>
          <button id="apply-config" type="button" class="push primary" disabled=${Boolean(snap?.restarting)} onClick=${restart}>
            ${snap?.restarting ? 'Reiniciando…' : 'Reiniciar conexões'}
          </button>
        </div>
        <div id="system-alert" class="banner error" role="alert" hidden=${!fault}>
          ${fault && html`
            <strong>${faultTitle}</strong>
            ${outputFault ? outputReasons.map((line, i) => html`<span key=${i}>${line}</span>`) : html`<span>${faultReason}</span>`}
            <div class="banner-actions">
              ${outputFault
                ? html`
                  <button type="button" class="push primary" data-help="output" onClick=${() => setBannerHelp(open => !open)}>Como resolver</button>
                  <button type="button" class="push" data-retry onClick=${restart}>${snap?.restarting ? 'Tentando…' : 'Tentar novamente'}</button>
                  <span>depois de instalar</span>`
                : html`
                  <button type="button" class="push" data-retry onClick=${restart}>${snap?.restarting ? 'Tentando…' : 'Tentar novamente'}</button>
                  <button type="button" class="link" data-help="bluetooth" onClick=${() => setBannerHelp(open => !open)}>Como resolver</button>`}
            </div>`}
        </div>
        ${fault && bannerHelp && html`<${HelpParagraph} kind=${outputFault ? 'output' : 'bluetooth'} snap=${snap} />`}

        <section id="panel-controllers" role="tabpanel" aria-labelledby="tab-controllers" hidden=${view !== 'controllers'}>
          <header class="page-header">
            <p id="controllers-subtitle">${subtitle}</p>
            <button id="connect" ref=${connectBtnRef} type="button" class="push" onClick=${openDiscovery}>Conectar controle</button>
          </header>
          <p id="platform-notice" class="note" hidden=${!mac}>No macOS não há controle Xbox virtual: o Pad2Go envia botões, analógicos e movimento para emuladores (como o Eden) via DSU.</p>

          <${Discovery} snap=${snap} open=${discoveryOpen} kind=${discoveryKind} setKind=${setDiscoveryKind}
            onClose=${closeDiscovery} onAcceptForeign=${acceptForeign} onOpenBluetooth=${openBluetoothSettings} discoveryRef=${discoveryRef} />

          <h2 class="group-title"><span>Jogadores</span><span id="player-count">${players.length ? `${players.length} ${players.length === 1 ? 'conectado' : 'conectados'}` : ''}</span></h2>
          <${DeviceList} snap=${snap} draft=${draft} detail=${detail} setDetail=${setDetail}
            onSetPlayerOutput=${setPlayerOutput} onDisconnect=${disconnectPlayer} onUpdateHold=${updateDeviceHold} />
          <p class="group-footer">Precisa parear um controle? <button id="show-howto" type="button" class="link" onClick=${openDiscovery}>Como conectar</button></p>

          <h2 class="group-title">Conexão</h2>
          <div class="group">
            <div class="row"><span class="row-icon"><${Icon} name="bluetooth" /></span><div class="row-label"><span>Bluetooth</span><small id="bluetooth-state">${bluetoothState}</small></div></div>
            <div class="row">
              <span class="row-icon"><${Icon} name="monitor" /></span>
              <div class="row-label"><span>Saída padrão</span><small id="output-detail">${snap?.output?.detail || ''}</small></div>
              <div class="row-control">
                <select id="output-default" ref=${defaultOutputRef} aria-label="Saída padrão" value=${defaultOutputValue}
                  onChange=${event => setDefaultOutput(event.currentTarget.value, false)}>
                  <option value="auto">Automática</option>
                  <option value="xbox" disabled=${mac}>Xbox 360</option>
                  <option value="dualsense" disabled=${mac}>DualSense</option>
                  <option value="switch" disabled=${mac}>Switch Pro</option>
                  <option value="none">Somente movimento</option>
                </select>
                <button type="button" class="push small" id="apply-output" disabled=${!players.length}
                  title="Aplicar aos players atuais (mantém escolhas manuais)"
                  onClick=${() => setDefaultOutput(defaultOutputRef.current?.value || defaultOutputValue, true)}>Aplicar</button>
              </div>
            </div>
            <label class="row">
              <div class="row-label"><span>Unir Joy-Cons</span><small>Um par, um jogador</small></div>
              <div class="row-control"><input type="checkbox" switch class="switch" data-config="combine" checked=${Boolean(draft?.combine)} onChange=${event => updateCheckboxOrSelect('combine', event.currentTarget.checked)} /></div>
            </label>
            <label class="row">
              <div class="row-label">
                <span>Desconexão automática</span>
                <small>0 desativa; 1–120 minutos sem atividade</small>
                ${errors.autoDisconnectMinutes && html`<small class="field-error">${errors.autoDisconnectMinutes}</small>`}
              </div>
              <div class="row-control">
                <${ValidatedInput} type="number" min="0" max="120" step="1" data-config="autoDisconnectMinutes"
                  aria-label="Desconexão automática em minutos" value=${draft?.autoDisconnectMinutes ?? 0}
                  error=${errors.autoDisconnectMinutes} onCommit=${val => commitConfigField('autoDisconnectMinutes', val, true)} />
              </div>
            </label>
            <label class="row">
              <div class="row-label"><span>Movimento para emuladores</span><small>Giroscópio via DSU</small></div>
              <div class="row-control"><input type="checkbox" switch class="switch" data-config="gyro" checked=${Boolean(draft?.gyro)} onChange=${event => updateCheckboxOrSelect('gyro', event.currentTarget.checked)} /></div>
            </label>
          </div>
        </section>

        <section id="panel-motion" role="tabpanel" aria-labelledby="tab-motion" hidden=${view !== 'motion'}>
          <header class="page-header"><p>Para Eden, Ryujinx, Cemu, Dolphin e outros emuladores via CemuHook/DSU: giroscópio, botões e analógicos. Um par de Joy-Cons unido aparece como um único controle.</p></header>
          <div class="group">
            <label class="row">
              <div class="row-label"><span>Enviar movimento</span><small>Servidor CemuHook / DSU</small></div>
              <div class="row-control"><input type="checkbox" switch class="switch" data-config="gyro" checked=${Boolean(draft?.gyro)} onChange=${event => updateCheckboxOrSelect('gyro', event.currentTarget.checked)} /></div>
            </label>
            <label class="row">
              <div class="row-label">
                <span>Touchpad do DualSense</span>
                <small id="touchpad-pointer-note">${draft?.gyro
                  ? 'Deslize com um dedo para mover o ponteiro; ao soltar, o giroscópio volta a controlar o movimento.'
                  : 'Ative Enviar movimento / DSU para usar o ponteiro pelo touchpad.'}</small>
              </div>
              <div class="row-control">
                <input id="touchpad-pointer" type="checkbox" switch class="switch" data-config="touchpadPointer"
                  disabled=${!draft?.gyro} checked=${Boolean(draft?.touchpadPointer)}
                  onChange=${event => updateCheckboxOrSelect('touchpadPointer', event.currentTarget.checked)} />
              </div>
            </label>
            <div class="row">
              <div class="row-label"><span>Servidor</span><small id="motion-address">${motionAddress}</small></div>
              <div class="row-control"><span id="motion-state" class=${motionStateClass}>${motionStateText}</span><button id="copy-address" type="button" class="push" onClick=${copyAddress}>Copiar endereço</button></div>
            </div>
            <div class="row">
              <div class="row-label"><span>Emuladores recebendo</span><small>Inscrições ativas. O protocolo não informa o nome do emulador.</small></div>
              <div class="row-control"><span id="client-count" class="value">${dsu.clients}</span></div>
            </div>
          </div>
          <p id="copy-result" class="group-footer" role="status">${copyResult}</p>

          <h2 class="group-title">Ajustes</h2>
          <div class="group">
            <div class="row">
              <div class="row-label"><label for="sensitivity">Sensibilidade horizontal</label><small>Níveis 1–5 ajustam só a rotação horizontal.</small></div>
              <div class="row-control slider">
                <input id="sensitivity" type="range" min="1" max="5" step="1" data-config="sensitivity"
                  value=${sensitivity} onInput=${event => updateRange('sensitivity', event.currentTarget.value)} />
                <output id="sensitivity-value" for="sensitivity">${`${sensitivity} · ${(1 + (sensitivity - 1) / 12).toFixed(2).replace('.', ',')}×`}</output>
              </div>
            </div>
            <div class="row">
              <div class="row-label"><label for="pair-gyro">Giroscópio do par de Joy-Cons</label><small>Qual Joy-Con move a câmera/mira quando estão unidos</small></div>
              <div class="row-control">
                <select id="pair-gyro" data-config="pairGyro" value=${draft?.pairGyro || 'Right'} onChange=${event => updateCheckboxOrSelect('pairGyro', event.currentTarget.value)}>
                  <option value="Right">Direito</option>
                  <option value="Left">Esquerdo</option>
                </select>
              </div>
            </div>
            <div class="row">
              <div class="row-label"><label for="host">Endereço do servidor</label><small class="field-error" id="host-error">${errors.host || ''}</small></div>
              <div class="row-control">
                <${ValidatedInput} id="host" type="text" data-config="host" spellcheck="false" aria-describedby="host-error"
                  value=${draft?.host ?? ''} error=${errors.host} onCommit=${val => commitConfigField('host', val, false)} />
              </div>
            </div>
            <div class="row">
              <div class="row-label"><label for="port">Porta</label><small class="field-error" id="port-error">${errors.port || ''}</small></div>
              <div class="row-control">
                <${ValidatedInput} id="port" type="number" min="1" max="65535" data-config="port" aria-describedby="port-error"
                  value=${draft?.port ?? ''} error=${errors.port} onCommit=${val => commitConfigField('port', val, true)} />
              </div>
            </div>
          </div>
          <p class="group-footer">Use o mesmo endereço e porta nas configurações de movimento do emulador. Alterações valem após reiniciar as conexões.</p>
        </section>

        <section id="panel-mapping" role="tabpanel" aria-labelledby="tab-mapping" hidden=${view !== 'mapping'}>
          <div id="mapping-root"><${Mapping} /></div>
        </section>

        <section id="panel-settings" role="tabpanel" aria-labelledby="tab-settings" hidden=${view !== 'settings'}>
          <header class="page-header"><p>Salvos automaticamente. Valem após reiniciar as conexões.</p></header>

          <h2 class="group-title">Botões e vibração</h2>
          <div class="group">
            <div class="row">
              <div class="row-label">
                <span>Layout A/B/X/Y</span>
                <small id="layout-hint">${draft?.layout === 'Switch' ? 'Pela letra: A do Switch → A do Xbox' : 'Pela posição: B do Switch → A do Xbox'}</small>
              </div>
              <div class="row-control">
                <div class="segmented" role="radiogroup" aria-label="Layout dos botões">
                  <button type="button" role="radio" data-layout="Xbox" aria-checked=${String((draft?.layout || 'Xbox') === 'Xbox')}
                    onClick=${() => updateCheckboxOrSelect('layout', 'Xbox')}>Posição</button>
                  <button type="button" role="radio" data-layout="Switch" aria-checked=${String(draft?.layout === 'Switch')}
                    onClick=${() => updateCheckboxOrSelect('layout', 'Switch')}>Letra</button>
                </div>
              </div>
            </div>
            <div class="row">
              <div class="row-label"><label for="vibration">Vibração</label><small>Força do retorno dos jogos. Padrão: 100%.</small></div>
              <div class="row-control slider">
                <input id="vibration" type="range" min="0" max="10" data-config="vibration"
                  value=${vibration} onInput=${event => updateRange('vibration', event.currentTarget.value)} />
                <output id="vibration-value" for="vibration">${`${vibration * 20}%`}</output>
              </div>
            </div>
          </div>

          <h2 class="group-title">Analógicos e gatilhos</h2>
          <div class="group">
            ${[['joycon', 'Joy-Con'], ['pro', 'Pro Controller'], ['gc', 'GameCube'], ['dualsense', 'DualSense']].map(([kind, label]) => html`
              <div key=${kind} class="row">
                <div class="row-label">
                  <label for=${`dz-${kind}`}>Zona morta · ${label}</label>
                  ${errors[`dz-${kind}`] && html`<small class="field-error">${errors[`dz-${kind}`]}</small>`}
                </div>
                <div class="row-control">
                  <${ValidatedInput} id=${`dz-${kind}`} type="number" min="0" max="50" data-deadzone=${kind}
                    value=${draft?.deadzones?.[kind] ?? ''} error=${errors[`dz-${kind}`]}
                    onCommit=${val => commitDeadzone(kind, val)} />
                  <span class="unit">%</span>
                </div>
              </div>`)}
            <div class="row">
              <div class="row-label"><label for="trigger">Gatilhos · GameCube</label></div>
              <div class="row-control">
                <select id="trigger" data-config="trigger" value=${draft?.trigger || '100% at Bump'} onChange=${event => updateCheckboxOrSelect('trigger', event.currentTarget.value)}>
                  <option value="100% at Bump">Máximo no clique</option>
                  <option value="100% at Max">Máximo no fim do curso</option>
                  <option value="Hair Trigger">Toque leve</option>
                </select>
              </div>
            </div>
          </div>

          <h2 class="group-title">Iluminação do DualSense</h2>
          <div class="group">
            <label class="row">
              <div class="row-label"><span>Personalizar iluminação</span><small>Aplicada ao reconectar após reiniciar as conexões</small></div>
              <div class="row-control"><input type="checkbox" class="switch" data-config="dualSenseLighting" checked=${Boolean(draft?.dualSenseLighting)} onChange=${event => updateCheckboxOrSelect('dualSenseLighting', event.currentTarget.checked)} /></div>
            </label>
            <div class="row">
              <div class="row-label"><label for="ds-color">Cor da barra</label></div>
              <div class="row-control"><input id="ds-color" type="color" data-config="dualSenseColor" value=${draft?.dualSenseColor || '#0080ff'} onChange=${event => updateCheckboxOrSelect('dualSenseColor', event.currentTarget.value)} /></div>
            </div>
            <div class="row">
              <div class="row-label">
                <label for="ds-brightness">Brilho da barra</label>
                ${errors.dualSenseBrightness && html`<small class="field-error">${errors.dualSenseBrightness}</small>`}
              </div>
              <div class="row-control">
                <${ValidatedInput} id="ds-brightness" type="number" min="0" max="100" data-config="dualSenseBrightness"
                  value=${draft?.dualSenseBrightness ?? 100} error=${errors.dualSenseBrightness}
                  onCommit=${val => commitConfigField('dualSenseBrightness', val, true)} />
                <span class="unit">%</span>
              </div>
            </div>
            <label class="row">
              <div class="row-label"><span>LEDs de jogador</span></div>
              <div class="row-control"><input type="checkbox" class="switch" data-config="dualSensePlayerLED" checked=${Boolean(draft?.dualSensePlayerLED)} onChange=${event => updateCheckboxOrSelect('dualSensePlayerLED', event.currentTarget.checked)} /></div>
            </label>
          </div>

          <h2 class="group-title">Gatilhos adaptativos do DualSense</h2>
          <div class="group">
            <div class="row">
              <div class="row-label"><label for="ds-trigger-l">L2</label></div>
              <div class="row-control">
                <select id="ds-trigger-l" data-config="dualSenseTriggerLeft" value=${draft?.dualSenseTriggerLeft || 'off'} onChange=${event => updateCheckboxOrSelect('dualSenseTriggerLeft', event.currentTarget.value)}>
                  <option value="off">Desligado</option>
                  <option value="resistance">Resistência constante</option>
                  <option value="barrier">Barreira</option>
                </select>
              </div>
            </div>
            <div class="row">
              <div class="row-label"><label for="ds-trigger-r">R2</label></div>
              <div class="row-control">
                <select id="ds-trigger-r" data-config="dualSenseTriggerRight" value=${draft?.dualSenseTriggerRight || 'off'} onChange=${event => updateCheckboxOrSelect('dualSenseTriggerRight', event.currentTarget.value)}>
                  <option value="off">Desligado</option>
                  <option value="resistance">Resistência constante</option>
                  <option value="barrier">Barreira</option>
                </select>
              </div>
            </div>
          </div>
          <p class="group-footer">Efeitos manuais aplicados após reiniciar as conexões.</p>

          <h2 class="group-title">Conexão</h2>
          <div class="group">
            <div class="row">
              <div class="row-label"><label for="max">Máximo de jogadores</label></div>
              <div class="row-control">
                <select id="max" data-config="max" value=${String(draft?.max ?? 4)} onChange=${event => commitConfigField('max', event.currentTarget.value, true)}>
                  <option value="4">4</option>
                  <option value="8">8</option>
                </select>
              </div>
            </div>
            <div class="row">
              <div class="row-label"><label for="host-mac">Endereço Bluetooth do PC</label><small class="field-error" id="hostMac-error">${errors.hostMac || ''}</small></div>
              <div class="row-control">
                <${ValidatedInput} id="host-mac" type="text" data-config="hostMac" spellcheck="false" aria-describedby="hostMac-error"
                  placeholder=${mac ? 'Necessário para parear' : 'Detectar automaticamente'}
                  value=${draft?.hostMac ?? ''} error=${errors.hostMac} onCommit=${val => commitConfigField('hostMac', val, false)} />
              </div>
            </div>
            <div class="row">
              <div class="row-label"><label for="hold">Joy-Con avulso</label><small>Posição padrão</small></div>
              <div class="row-control">
                <select id="hold" data-config="hold" value=${draft?.hold || 'Vertical'} onChange=${event => updateCheckboxOrSelect('hold', event.currentTarget.value)}>
                  <option value="Vertical">Vertical</option>
                  <option value="Horizontal">Horizontal</option>
                </select>
              </div>
            </div>
            <label class="row">
              <div class="row-label"><span>Vibrar ao conectar</span></div>
              <div class="row-control"><input type="checkbox" switch class="switch" data-config="haptics" checked=${Boolean(draft?.haptics)} onChange=${event => updateCheckboxOrSelect('haptics', event.currentTarget.checked)} /></div>
            </label>
            <label class="row">
              <div class="row-label"><span>Aceitar controles de outro PC</span><small>Conectar sem parear de novo</small></div>
              <div class="row-control"><input type="checkbox" switch class="switch" data-config="foreign" checked=${Boolean(draft?.foreign)} onChange=${event => updateCheckboxOrSelect('foreign', event.currentTarget.checked)} /></div>
            </label>
            <label class="row">
              <div class="row-label"><span>Avisos de conexão</span><small>Mostrar quando um controle conecta ou desconecta</small></div>
              <div class="row-control"><input id="connection-notifications" type="checkbox" switch class="switch" checked=${notifications} onChange=${event => toggleNotifications(event.currentTarget.checked)} /></div>
            </label>
          </div>

          <h2 class="group-title"><span>Verificação do sistema</span><button id="check-system" type="button" class="push small" onClick=${() => diagnose(true)}>Verificar</button></h2>
          <div id="diagnostic-results" class="group">
            ${diagnostics.map(row => html`
              <div key=${row.name} class="row">
                <span class=${`row-icon${row.ok ? '' : ' missing'}`}><${Icon} name=${row.ok ? 'check' : 'warn'} /></span>
                <div class="row-label"><span>${row.name}</span><small>${row.detail}</small></div>
                ${!row.ok && row.help && html`<div class="row-control"><button type="button" class="link" data-help=${row.help}
                  onClick=${() => setDiagHelp(current => ({ ...current, [row.name]: !current[row.name] }))}>Como resolver</button></div>`}
              </div>
              ${!row.ok && row.help && diagHelp[row.name] && html`<${HelpParagraph} key=${`help-${row.name}`} kind=${row.help} snap=${snap} />`}`)}
          </div>
        </section>

        <p id="feedback" class=${`feedback${feedback.error ? ' error' : ''}`} role=${feedback.error ? 'alert' : 'status'} aria-live="polite">${feedback.message}</p>
      </div>
    </main>`;
}

render(html`<${App} />`, document.getElementById('pad2go'));
render(html`<${TestDialog} />`, document.getElementById('test-root'));
render(html`<${Toasts} />`, document.getElementById('connection-toasts'));
