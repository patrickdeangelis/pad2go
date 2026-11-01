/* Mapeamento tab (Preact + htm, vendored, no build step). State lives in
   app.js: window.pad2go exposes the snapshot/draft, a save path and change
   notifications, so this module never keeps its own copy of the settings. */
import { html, render, useState, useEffect, useRef } from './vendor/htm-preact.module.js';

const remapLabels = { Default: 'Padrão', None: 'Sem ação', MINUS: '−', PLUS: '+', L_STK: 'Clique analógico L', R_STK: 'Clique analógico R', UP: 'Cima', DOWN: 'Baixo', LEFT: 'Esquerda', RIGHT: 'Direita', HOME: 'Home', CAPT: 'Captura' };
const remapValues = ['Default', 'None', 'A', 'B', 'X', 'Y', 'L', 'R', 'ZL', 'ZR', 'MINUS', 'PLUS', 'L_STK', 'R_STK', 'UP', 'DOWN', 'LEFT', 'RIGHT', 'HOME', 'CAPT'];
const extraRows = [['home', 'Home'], ['capt', 'Captura'], ['c', 'C (GameCube)'], ['gl', 'GL'], ['gr', 'GR'], ['sll', 'SL do Joy-Con L'], ['srl', 'SR do Joy-Con L'], ['slr', 'SL do Joy-Con R'], ['srr', 'SR do Joy-Con R']];
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
const dsTargets = [['Default','Padrão'],['None','Sem ação'],['B','A'],['A','B'],['Y','X'],['X','Y'],['L','LB'],['R','RB'],['ZL','LT'],['ZR','RT'],['MINUS','View'],['PLUS','Menu'],['L_STK','Analógico esquerdo'],['R_STK','Analógico direito'],['UP','Direcional cima'],['DOWN','Direcional baixo'],['LEFT','Direcional esquerda'],['RIGHT','Direcional direita'],['HOME','Xbox']];
const dsRows = [['cross','Cruz'],['circle','Círculo'],['square','Quadrado'],['triangle','Triângulo'],['l1','L1'],['r1','R1'],['l2','L2'],['r2','R2'],['l3','L3'],['r3','R3'],['create','Criar'],['options','Opções'],['ps','PS'],['touch','Clique do touchpad'],['up','Direcional cima'],['down','Direcional baixo'],['left','Direcional esquerda'],['right','Direcional direita']];

// The 31 output-remap sources under their headings, each exactly once.
const groups = [
  ['Face', ['A', 'B', 'X', 'Y']],
  ['Direcionais', ['UP', 'DOWN', 'LEFT', 'RIGHT']],
  ['Ombros e gatilhos', ['L', 'R', 'ZL', 'ZR']],
  ['Sistema', ['MINUS', 'PLUS', 'HOME', 'CAPT', 'C', 'MUTE']],
  ['Analógicos', ['L_STK', 'R_STK']],
  ['Joy-Con', ['SL_L', 'SR_L', 'SL_R', 'SR_R']],
  ['GameCube', ['GL', 'GR', 'GC_L_CLICK', 'GC_R_CLICK']],
  ['Touchpad do DualSense', ['PS_L_Touch', 'PS_R_Touch', 'PS_C_Click']],
];
const scopes = [['global', 'Global'], ['xbox', 'Xbox 360'], ['dualsense', 'DualSense'], ['switch', 'Switch Pro']];
const scopeName = scope => scopes.find(([value]) => value === scope)[1];
const label = value => outputRemapLabels[value] || value;
const set = target => Boolean(target) && target !== 'Default';

function useStore() {
  const [, setTick] = useState(0);
  useEffect(() => window.pad2go.onChange(() => setTick(tick => tick + 1)), []);
  return window.pad2go.state();
}

function Row({ id, name, value, options, onChange }) {
  return html`<div class="row"><div class="row-label"><label for=${id}>${name}</label></div><div class="row-control"><select id=${id} value=${value} onChange=${event => onChange(event.currentTarget.value)}>${options.map(([v, text]) => html`<option value=${v}>${text}</option>`)}</select></div></div>`;
}

function Physical({ draft }) {
  const extraOptions = remapValues.map(value => [value, remapLabels[value] || value]);
  return html`
    <h2 class="group-title">No controle físico</h2>
    <h3 class="subgroup-title">Botões extras</h3>
    <div class="group">${extraRows.map(([key, name]) => html`<${Row} key=${key} id=${`remap-${key}`} name=${name} value=${draft.remaps?.[key] || 'Default'} options=${extraOptions}
      onChange=${value => window.pad2go.update(d => { d.remaps = { ...d.remaps, [key]: value }; })} />`)}</div>
    <p class="group-footer">Redirecione botões extras para outro botão do controle.</p>
    <h3 class="subgroup-title">Botões do DualSense</h3>
    <div class="group">${dsRows.map(([key, name]) => html`<${Row} key=${key} id=${`ds-remap-${key}`} name=${name} value=${draft.dualSenseRemaps?.[key] || 'Default'} options=${dsTargets}
      onChange=${value => window.pad2go.update(d => { d.dualSenseRemaps = { ...d.dualSenseRemaps, [key]: value }; })} />`)}</div>
    <p class="group-footer">Escolha a ação no controle Xbox virtual. L2/R2 mapeados para botões acionam a partir de 50% do curso. As alterações entram em vigor ao reiniciar.</p>`;
}

function ScopeControl({ scope, setScope }) {
  const refs = useRef([]);
  const move = (event, index) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    const next = (index + (event.key === 'ArrowRight' ? 1 : -1) + scopes.length) % scopes.length;
    setScope(scopes[next][0]);
    refs.current[next]?.focus();
  };
  return html`<div class="segmented" role="radiogroup" aria-label="Saída do mapeamento">${scopes.map(([value, name], index) => html`
    <button type="button" role="radio" ref=${node => { refs.current[index] = node; }} aria-checked=${String(value === scope)} tabindex=${value === scope ? 0 : -1}
      onClick=${() => setScope(value)} onKeyDown=${event => move(event, index)}>${name}</button>`)}</div>`;
}

function ConfirmReset({ scope, count, onCancel, onConfirm }) {
  const dialog = useRef(null);
  useEffect(() => { dialog.current?.showModal(); }, []);
  const text = count === 1 ? '1 botão volta ao padrão.' : `${count} botões voltam ao padrão.`;
  return html`<dialog ref=${dialog} class="confirm" aria-labelledby="confirm-reset-title" onClose=${onCancel}>
    <h2 id="confirm-reset-title">Restaurar ${scopeName(scope)}?</h2>
    <p>${text} As outras saídas não mudam.</p>
    <footer><button type="button" class="push" onClick=${onCancel}>Cancelar</button><button type="button" class="push primary" onClick=${onConfirm}>Restaurar</button></footer>
  </dialog>`;
}

function Output({ draft }) {
  const [scope, setScope] = useState('global');
  const [confirming, setConfirming] = useState(false);
  const table = draft.outputRemaps?.[scope] || {};
  const global = draft.outputRemaps?.global || {};
  const count = Object.values(table).filter(set).length;
  const setCell = (source, value) => window.pad2go.update(d => {
    const next = { ...(d.outputRemaps?.[scope] || {}) };
    if (value === 'Default') delete next[source]; else next[source] = value;
    d.outputRemaps = { ...(d.outputRemaps || {}), [scope]: next };
  });
  const reset = () => {
    setConfirming(false);
    window.pad2go.update(d => { d.outputRemaps = { ...(d.outputRemaps || {}), [scope]: {} }; });
  };
  return html`
    <h2 class="group-title">Na saída virtual</h2>
    <div class="remap-toolbar">
      <${ScopeControl} scope=${scope} setScope=${setScope} />
      <button type="button" class="push small" disabled=${count === 0} onClick=${() => setConfirming(true)}>Restaurar ${scopeName(scope)}</button>
    </div>
    ${groups.map(([heading, sources]) => html`
      <h3 class="subgroup-title" key=${heading}>${heading}</h3>
      <div class="group remap-grid">${sources.map(source => {
        const own = set(table[source]) ? table[source] : '';
        const inherited = scope !== 'global' && !own && set(global[source]) ? global[source] : '';
        return html`<div class="remap-field" key=${source}>
          <span class="remap-source"><label for=${`out-remap-${source}`}>${label(source)}</label><small>${inherited ? `Global: ${label(inherited)}` : ''}</small></span>
          <select id=${`out-remap-${source}`} value=${own || 'Default'} onChange=${event => setCell(source, event.currentTarget.value)}>${outputRemapTargets.map(value => html`<option value=${value}>${label(value)}</option>`)}</select>
        </div>`;
      })}</div>`)}
    <p class="group-footer">O mapa global vale para todas as saídas; cada saída pode sobrescrever os botões que precisar. “Padrão” usa o mapa embutido (ou o global), “Sem ação” desliga o botão e alvos sem equivalente na saída são ignorados. A/B/X/Y seguem a posição do layout Switch (B é o botão de baixo). As alterações valem após reiniciar as conexões.</p>
    ${confirming && html`<${ConfirmReset} scope=${scope} count=${count} onCancel=${() => setConfirming(false)} onConfirm=${reset} />`}`;
}

function Mapping() {
  const { draft } = useStore();
  if (!draft) return null;
  return html`
    <header class="page-header"><p>Os botões passam por duas etapas: primeiro no controle físico, depois na saída virtual.</p></header>
    <${Physical} draft=${draft} />
    <${Output} draft=${draft} />`;
}

render(html`<${Mapping} />`, document.getElementById('mapping-root'));
