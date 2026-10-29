'use strict';
const $ = id => document.getElementById(id);
const tabs = [...document.querySelectorAll('[role=tab]')];
function selectTab(tab) {
  for (const item of tabs) {const active = item === tab;item.setAttribute('aria-selected', String(active));item.tabIndex = active ? 0 : -1;$(item.getAttribute('aria-controls')).hidden = !active;}
}
tabs.forEach((tab, i) => {
  tab.addEventListener('click', () => selectTab(tab));
  tab.addEventListener('keydown', event => {let next;if (event.key === 'ArrowRight') next = (i + 1) % tabs.length;if (event.key === 'ArrowLeft') next = (i + tabs.length - 1) % tabs.length;if (event.key === 'Home') next = 0;if (event.key === 'End') next = tabs.length - 1;if (next !== undefined) {event.preventDefault();selectTab(tabs[next]);tabs[next].focus();}});
});
let config = null;
let volumeDirty = false;
$('volume').addEventListener('input', () => {volumeDirty = true;$('volume-value').textContent = `${$('volume').value} %`;});
$('volume-form').addEventListener('submit', async event => {
  event.preventDefault();const button = event.submitter;button.disabled = true;
  try {const result = await request('/api/volume', {method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify({volume:Number($('volume').value)})});volumeDirty = false;feedback('volume-feedback', `${result.volume} % ab der nächsten Meldung.`);}
  catch (error) {feedback('volume-feedback', error.message, true);}
  finally {button.disabled = false;}
});
async function request(path, options) {
  const response = await fetch(path, options);
  const body = await response.json();
  if (!response.ok) throw new Error(body.error || `HTTP ${response.status}`);
  return body;
}
function feedback(id, text, error = false) {const el = $(id);el.textContent = text;el.classList.toggle('error', error);}
async function submit(button, feedbackID, action) {
  const old = button.textContent;button.disabled = true;button.textContent = 'Wird vorbereitet …';feedback(feedbackID, '');
  try {const result = await action();feedback(feedbackID, `„${result.job.label}“ zur Warteschlange hinzugefügt.`);poll();}
  catch (error) {feedback(feedbackID, error.message, true);}
  finally {button.disabled = false;button.textContent = old;}
}
function renderSounds(sounds) {
  $('sound-list').replaceChildren();$('sound-count').textContent = `${sounds.length} Sounds`;
  if (!sounds.length) {const p = document.createElement('p');p.className = 'empty';p.textContent = 'Noch keine Sounds konfiguriert. URL und Upload sind bereits nutzbar.';$('sound-list').append(p);}
  for (const sound of sounds) {
    const row = document.createElement('div');row.className = 'sound';
    const info = document.createElement('div');info.className = 'sound-info';
    const name = document.createElement('strong');name.textContent = sound.id;
    const file = document.createElement('small');file.textContent = sound.filename;info.append(name, file);
    const button = document.createElement('button');button.type = 'button';button.textContent = '▶ Abspielen';button.setAttribute('aria-label', `${sound.id} abspielen`);
    button.addEventListener('click', () => submit(button, 'sounds-feedback', () => request(`/play/${encodeURIComponent(sound.id)}`, {method: 'POST'})));
    row.append(info, button);$('sound-list').append(row);
  }
}
$('url-form').addEventListener('submit', event => {event.preventDefault();const url = $('audio-url').value.trim();if (!/^https?:\/\//i.test(url)) {feedback('url-feedback', 'Bitte eine HTTP- oder HTTPS-URL eingeben.', true);return;}submit(event.submitter, 'url-feedback', () => request('/play-url', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({url})}));});
function selectedFile() {const file = $('audio-file').files[0];$('file-label').textContent = file ? file.name : 'Datei auswählen oder hier ablegen';}
$('audio-file').addEventListener('change', selectedFile);
for (const name of ['dragenter', 'dragover']) $('drop').addEventListener(name, event => {event.preventDefault();$('drop').classList.add('dragging');});
for (const name of ['dragleave', 'drop']) $('drop').addEventListener(name, () => $('drop').classList.remove('dragging'));
$('drop').addEventListener('drop', event => {event.preventDefault();if (event.dataTransfer.files.length !== 1) {feedback('upload-feedback', 'Bitte genau eine Datei ablegen.', true);return;}$('audio-file').files = event.dataTransfer.files;selectedFile();});
$('upload-form').addEventListener('submit', event => {event.preventDefault();const file = $('audio-file').files[0];if (!file) return;if (config && file.size > config.max_file_size_mb * 1024 * 1024) {feedback('upload-feedback', 'Die Datei überschreitet das Größenlimit.', true);return;}const data = new FormData();data.append('file', file);submit(event.submitter, 'upload-feedback', () => request('/play-file', {method: 'POST', body: data}));});
let lastHistory = '';
let polling = false;
let statusEpoch = 0;
function renderHistory(s) {
 $('last-error').hidden = !s.last_error;
 $('last-error').textContent = s.last_error ? `Letzter Fehler: ${s.last_error}` : '';
 $('clear-history').disabled = !s.history.length && !s.last_error;
 const key = JSON.stringify(s.history);
 if (key === lastHistory) return;
 lastHistory = key;
 $('history').replaceChildren();
 if (!s.history.length) {const li = document.createElement('li');li.className='empty';li.textContent='Noch keine Meldungen.';$('history').append(li);return;}
 for (const e of s.history) {
  const li=document.createElement('li');li.className=`history-row ${e.state}`;
  const result=document.createElement('span');result.className='result';result.textContent=e.state==='completed'?'✓':'!';result.setAttribute('aria-label',e.state==='completed'?'Abgeschlossen':'Fehlgeschlagen');
  const details=document.createElement('div');details.className='details';
  const label=document.createElement('strong');label.textContent=e.label;details.append(label);
  if(e.error){const error=document.createElement('small');error.textContent=e.error;details.append(error);}
  const time=document.createElement('time');time.dateTime=e.at;time.textContent=new Date(e.at).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'});
  li.append(result,details,time);$('history').append(li);
 }
}
$('clear-history').addEventListener('click',async()=>{
 const button=$('clear-history');button.disabled=true;
 try {await request('/api/history',{method:'DELETE'});statusEpoch++;renderHistory({history:[],last_error:''});feedback('history-feedback','Meldungen gelöscht.');poll();}
 catch(error){feedback('history-feedback',error.message,true);button.disabled=false;}
});
async function poll() {
  if (polling) return;polling = true;const epoch = statusEpoch;
  try {
    const s = await request('/api/status');if(epoch!==statusEpoch)return;$('connection').textContent = 'Verbunden';$('connection').classList.remove('offline');
    if (!volumeDirty) {$('volume').value = s.volume;$('volume-value').textContent = `${s.volume} %`;}
    $('state').textContent = ({idle:'Bereit', starting:'Wird gestartet', playing:'Wiedergabe', restoring:'Wird wiederhergestellt'})[s.state] || s.state;
    $('current').textContent = s.current?.label || 'Keine Wiedergabe';$('queued').textContent = s.queue;$('pulse').classList.toggle('active', s.state !== 'idle');
    $('mqtt').textContent = s.mqtt === 'disabled' ? 'MQTT aus' : `MQTT: ${{connected:'verbunden', connecting:'verbindet …', disconnected:'getrennt', 'subscription failed':'Abonnement fehlgeschlagen'}[s.mqtt] || s.mqtt}`;
    renderHistory(s);
  } catch { $('connection').textContent = 'Nicht erreichbar';$('connection').classList.add('offline'); }
  finally {polling = false;}
}
async function init() {
  try {const [sounds, settings] = await Promise.all([request('/api/sounds'), request('/api/config')]);config = settings;renderSounds(sounds);$('limits').textContent = `Max. ${settings.max_file_size_mb} MiB · ${settings.max_audio_duration_seconds} s Audio`;$('version').textContent = `snapnotify ${settings.version}`;}
  catch (error) {feedback('sounds-feedback', 'Konfiguration konnte nicht geladen werden. Seite neu laden.', true);}
  poll();setInterval(() => {if (!document.hidden) poll();}, 1000);document.addEventListener('visibilitychange', () => {if (!document.hidden) poll();});
}
init();
