/* Input test dialog (Preact + htm). The structure, readings and virtual Xbox
   pad are Preact; the controller SVG art is injected once per open and its
   pressed parts are toggled in a layout effect, because those nodes come
   from the SVG files, not from this component. Samples arrive at stream rate
   and are drawn at most once per animation frame. */
import { html, useState, useEffect, useLayoutEffect, useRef } from './vendor/htm-preact.module.js';

const controllerAsset = kind => ({ ds: 'dualsense', left: 'joycon-l', right: 'joycon-r', pro: 'pro', gc: 'gamecube' }[kind] || 'pro');
const holdMode = member => {
  const { draft, snap } = window.pad2go.state();
  return draft?.holds?.[member?.addr] || draft?.hold || snap?.config?.holds?.[member?.addr] || snap?.config?.hold || 'Vertical';
};
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
function mappingText(slot) {
  const { draft, snap } = window.pad2go.state();
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
}
function testShoulders(kinds) {
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

const idle = { buttons: 0, pressed: [], lx: 0, ly: 0, rx: 0, ry: 0, lt: 0, rt: 0, analog: false, gyro: [0, 0, 0] };
const outputLabels = {
  xbox: { heading: 'Xbox 360', l: 'LB', r: 'RB', back: 'Back', guide: '⌂', start: 'Start', y: 'Y', x: 'X', b: 'B', a: 'A' },
  dualsense: { heading: 'DualSense' },
  switch: { heading: 'Switch Pro' },
  none: { heading: 'Sem saída virtual' },
};
const dsNames = { Y: 'Square', X: 'Triangle', B: 'Cross', A: 'Circle', L: 'L1', R: 'R1', ZL: 'L2', ZR: 'R2', MINUS: 'Create', PLUS: 'Options', HOME: 'PS', MUTE: 'Mute', PS_C_Click: 'Clique do touchpad' };

async function virtualArt(kind) {
  if (kind !== 'dualsense' && kind !== 'switch') return '';
  let svg = await controllerSVG(kind === 'switch' ? 'pro' : 'ds');
  if (kind === 'switch') {
    // The virtual target is a Switch 1 Pro Controller (057E:2009), which has
    // Capture but not the Switch 2 Chat/C button present in the shared art.
    svg = svg.replace(/<g class="control" data-key="TOUCH"[\s\S]*?<\/g>/, '');
  }
  svg = svg.replace(/id="([^"]+)"/g, 'id="vo-$1"').replace(/url\(#([^)]+)\)/g, 'url(#vo-$1)');
  return `<div class="hardware-set virtual-set">${svg}</div>`;
}

// What one open of the dialog needs, built from the player slot at open time.
async function prepare(player) {
  const { snap } = window.pad2go.state();
  const slot = snap.players.find(p => p.player === player);
  const kinds = slot?.members.map(m => m.kind) || [];
  const testKind = kinds.length === 2 ? 'joy-pair' : kinds[0] || '';
  const horizontal = kinds.length === 1 && ['left', 'right'].includes(kinds[0]) && holdMode(slot.members[0]) === 'Horizontal';
  const effective = slot?.outputKind || 'none';
  const requested = slot?.outputOverrideKind || effective;
  const virtualKind = requested === 'auto' ? effective : requested;
  // Running output remaps (applied config), so the test proves them.
  const remaps = (slot?.activeRemaps || []).map(r => `${r.source} → ${r.target === 'None' ? 'Sem ação' : r.target}`);
  return {
    player, testKind, virtualKind,
    deviceName: slot?.members.length === 2 ? 'Par de Joy-Cons' : slot?.members[0]?.name || slot?.name || 'Controle',
    shoulders: testShoulders(kinds),
    art: await controllerTestArt(slot?.members || [], horizontal),
    artClass: `test-controller ${kinds.length === 2 ? 'joy-pair' : kinds[0] || ''}${horizontal ? ' joy-horizontal' : ''}`,
    mapping: mappingText(slot) + (remaps.length ? ` · Remapeado: ${remaps.join(', ')}` : ''),
    outputHeading: `Saída virtual · ${(outputLabels[virtualKind] || outputLabels.none).heading}${requested !== effective && effective === 'none' ? ' (indisponível)' : ''}`,
    virtual: await virtualArt(virtualKind),
  };
}

function XboxPad({ sample }) {
  const set = outputLabels.xbox;
  const key = (id, label, wide = false, title = '') => html`<span id=${`pad-${id}`} class=${`pad-key${wide ? ' wide' : ''}${sample.buttons & buttonIds[id] ? ' active' : ''}`} title=${title || undefined}>${label}</span>`;
  const stick = (id, x, y, bit) => html`<div class=${`stick-field${sample.buttons & bit ? ' active' : ''}`}><span class="stick-marker" id=${id} style=${{ transform: `translate(${x * 14}px,${-y * 14}px)` }}></span></div>`;
  return html`
    <div class="input-triggers">${key('l', set.l, true)}<span class="input-center">${key('back', set.back, true, 'Back (− e Captura)')}${key('guide', set.guide, false, 'Guide (Home)')}${key('start', set.start, true, 'Start (+)')}</span>${key('r', set.r, true)}</div>
    <div class="input-pad-body">
      <div class="pad-side">${stick('stick-left', sample.lx, sample.ly, 0x0040)}<div class="cross4">${key('up', '↑')}${key('left', '←')}${key('right', '→')}${key('down', '↓')}</div></div>
      <div class="pad-side"><div class="cross4">${key('y', set.y)}${key('x', set.x)}${key('b', set.b)}${key('a', set.a)}</div>${stick('stick-right', sample.rx, sample.ry, 0x0080)}</div>
    </div>`;
}

function Readings({ sample, testKind }) {
  const pressed = (sample.pressed || []).slice().sort((a, b) => (physicalOrder.indexOf(a) + 99) % 99 - (physicalOrder.indexOf(b) + 99) % 99);
  const names = testKind === 'ds' ? dsNames : {};
  const touches = (sample.touch || []).filter(point => point.active);
  const trigger = (side, raw) => {
    const value = Math.round(raw / 255 * 100);
    return html`<div class="row"><div class="row-label"><span>Gatilho ${side === 'l' ? 'LT' : 'RT'}</span></div><div class="row-control meter"><progress id=${`trigger-${side}`} max="100" value=${value}></progress><output id=${`trigger-${side}-value`} class="value">${value}%${sample.analog ? '' : ' · digital'}</output></div></div>`;
  };
  return html`<div class="group readings">
    <div class="row"><div class="row-label"><span>Analógico esquerdo</span></div><output id="left-values" class="value">X ${number(sample.lx)} · Y ${number(sample.ly)}</output></div>
    <div class="row"><div class="row-label"><span>Analógico direito</span></div><output id="right-values" class="value">X ${number(sample.rx)} · Y ${number(sample.ry)}</output></div>
    ${trigger('l', sample.lt)}
    ${trigger('r', sample.rt)}
    <div class="row"><div class="row-label"><span>Giroscópio (°/s)</span></div><output id="gyro-values" class="value">${testKind === 'ds' && !sample.motionCalibrated ? 'Calibração indisponível' : sample.gyro.map(v => number(v, 1)).join(' · ')}</output></div>
    <div class="row" id="touch-test" hidden=${testKind !== 'ds'}><div class="row-label"><span>Touchpad</span><small>Até dois contatos simultâneos</small><output id="touch-values" class="value">${touches.length ? touches.map(point => `#${point.id}: ${point.x}, ${point.y}`).join(' · ') : 'Sem contato'}</output></div>
      <svg class="touch-surface" viewBox="0 0 1920 1080" role="img" aria-label="Posição dos contatos no touchpad"><rect width="1920" height="1080" rx="100"/>${[0, 1].map(i => {
        const point = (sample.touch || [])[i];
        return html`<circle id=${`touch-point-${i}`} r="75" hidden=${!point?.active} cx=${point?.active ? Math.max(0, Math.min(1919, point.x)) : 0} cy=${point?.active ? Math.max(0, Math.min(1079, point.y)) : 0}/>`;
      })}</svg></div>
    <div class="row"><div class="row-label"><span>Botões no controle</span><small>O que você aperta, antes de virar o controle virtual</small></div><output id="pressed-values" class="value">${pressed.length ? pressed.map(name => names[name] || physicalLabels[name] || name.replace(/_/g, ' ')).join(' · ') : '—'}</output></div>
  </div>`;
}

// Pressed parts of the injected SVG art (physical controller, shoulders and
// the DualSense/Switch Pro virtual art).
function highlightArt(root, sample, testKind, virtualKind) {
  const pressed = sample.pressed || [];
  const physicalLT = sample.analog ? sample.plt : sample.lt;
  const physicalRT = sample.analog ? sample.prt : sample.rt;
  root.querySelectorAll('#test-controller .control[data-key]').forEach(node => node.classList.toggle('is-pressed', pressed.some(name => physicalKey(name, testKind) === node.dataset.key)));
  root.querySelectorAll('#test-controller .control[data-key="L2"], #test-controller .control[data-key="R2"]').forEach(node => node.classList.toggle('is-pressed', (node.dataset.key === 'L2' ? physicalLT : physicalRT) > 0));
  const sticks = testKind === 'joy-pair' ? (sample.physicalKind === 1 ? [['LS', sample.plx, sample.ply]] : sample.physicalKind === 2 ? [['RS', sample.prx, sample.pry]] : [])
    : testKind === 'left' ? [['LS', sample.plx, sample.ply]] : testKind === 'right' ? [['RS', sample.prx, sample.pry]]
    : [['LS', sample.plx || 0, sample.ply || 0], ['RS', sample.prx || 0, sample.pry || 0]];
  sticks.forEach(([key, x = 0, y = 0]) => root.querySelectorAll(`#test-controller [data-key="${key}"]`).forEach(node => { node.style.transformBox = 'fill-box'; node.style.transformOrigin = 'center'; node.style.transform = `translate(${x * 24}px,${-y * 24}px)`; }));
  root.querySelectorAll('#test-shoulders [data-input]').forEach(node => node.classList.toggle('is-active', pressed.some(name => physicalKey(name, testKind) === physicalKey(node.dataset.input, testKind))));
  root.querySelectorAll('#test-shoulders [data-trigger]').forEach(node => node.classList.toggle('is-active', (node.dataset.trigger === 'lt' ? physicalLT : physicalRT) > 0));
  const map = virtualKeyMap[virtualKind];
  if (!map) return;
  root.querySelectorAll('#test-output-controller .control[data-key]').forEach(node => {
    const key = node.dataset.key;
    let on = Boolean(sample.buttons & (map[key] || 0));
    if (key === 'L2' || key === 'ZL') on = sample.lt > 0;
    if (key === 'R2' || key === 'ZR') on = sample.rt > 0;
    if (key === 'CAPTURE') on = (sample.mapped || sample.pressed || []).includes('CAPT');
    if (key === 'MUTE') on = (sample.mapped || sample.pressed || []).includes('MUTE');
    node.classList.toggle('is-pressed', on);
  });
}

export function TestDialog() {
  const [view, setView] = useState(null);
  const [sample, setSample] = useState(idle);
  const [status, setStatus] = useState('Aguardando entradas.');
  const dialog = useRef(null);
  const latest = useRef(null);
  const frame = useRef(0);
  const noteUntil = useRef(0);
  const opening = useRef(0);

  const close = () => {
    const player = view?.player;
    setView(null);
    if (player != null) requestAnimationFrame(() => document.querySelector(`[data-test="${player}"]`)?.focus());
  };

  useEffect(() => {
    window.pad2go.openTest = async player => {
      const token = ++opening.current;
      const next = await prepare(player);
      if (token !== opening.current) return;
      setSample(idle); setStatus('Aguardando entradas.'); noteUntil.current = 0;
      setView(next);
    };
    window.pad2go.closeTest = () => { opening.current++; close(); };
    // A player that disconnects closes its test.
    return window.pad2go.onChange(() => {
      setView(current => current && !window.pad2go.state().snap?.players.some(p => p.player === current.player) ? null : current);
    });
  });

  // Stream: open with the dialog, closed when it goes away.
  useEffect(() => {
    if (!view) return undefined;
    dialog.current?.open || dialog.current?.showModal();
    const stream = new EventSource(`/api/players/${view.player}/input`);
    stream.addEventListener('input', event => {
      latest.current = JSON.parse(event.data);
      if (frame.current) return;
      frame.current = requestAnimationFrame(() => {
        frame.current = 0;
        setSample(latest.current);
        if (!noteUntil.current || Date.now() > noteUntil.current) { noteUntil.current = 0; setStatus('Recebendo entradas.'); }
      });
    });
    stream.onerror = () => setStatus('Sem entradas: o controle desconectou?');
    window.addEventListener('beforeunload', () => stream.close(), { once: true });
    return () => { stream.close(); cancelAnimationFrame(frame.current); frame.current = 0; };
  }, [view]);

  useLayoutEffect(() => { if (view && dialog.current) highlightArt(dialog.current, sample, view.testKind, view.virtualKind); }, [sample, view]);

  const rumble = async () => {
    try { await window.pad2go.api(`/api/players/${view.player}/rumble`, { method: 'POST' }); setStatus('Vibração enviada ao controle.'); } catch (error) { setStatus(`Não foi possível vibrar: ${error.message}`); }
    noteUntil.current = Date.now() + 2500;
  };

  if (!view) return null;
  const virtual = view.virtualKind === 'none'
    ? html`<div class="input-pad" id="test-output-controller" role="img" aria-label="Visualização das entradas recebidas"><div class="virtual-empty">Sem saída virtual: os jogos não recebem um controle virtual neste player.</div></div>`
    : view.virtualKind === 'xbox'
      ? html`<div class="input-pad" id="test-output-controller" role="img" aria-label="Visualização das entradas recebidas"><${XboxPad} sample=${sample} /></div>`
      : html`<div class="input-pad virtual-hardware" id="test-output-controller" role="img" aria-label="Visualização das entradas recebidas" dangerouslySetInnerHTML=${{ __html: view.virtual }}></div>`;
  return html`<dialog id="test-dialog" class="sheet" aria-labelledby="test-title" ref=${dialog} onClose=${close}>
    <header class="sheet-header"><div><h2 id="test-title">Teste de entrada · ${view.deviceName}</h2><p>Veja os botões físicos e o que chega ao controle virtual.</p></div></header>
    <section class="test-device-section" aria-label="Controle conectado">
      <div class="test-device-heading"><strong id="test-device-name">${view.deviceName}</strong><span>Entrada física</span></div>
      <div id="test-shoulders" class="test-shoulders" dangerouslySetInnerHTML=${{ __html: view.shoulders }}></div>
      <div id="test-controller" class=${view.artClass} dangerouslySetInnerHTML=${{ __html: view.art }}></div>
      <p id="test-mapping" class="test-mapping">${view.mapping}</p>
    </section>
    <div class="output-heading" id="test-output-heading" data-kind=${view.virtualKind}>${view.outputHeading}</div>
    <div class="input-layout">
      ${virtual}
      <${Readings} sample=${sample} testKind=${view.testKind} />
    </div>
    <footer class="sheet-footer"><span id="input-state" class="secondary" role="status">${status}</span><button id="rumble" type="button" class="push" onClick=${rumble}>Vibrar</button><button id="close-test" type="button" class="push primary" onClick=${() => dialog.current?.close()}>Concluído</button></footer>
  </dialog>`;
}

