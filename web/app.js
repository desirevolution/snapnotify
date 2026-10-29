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
let soundsKey = '';
let lastSoundsRefresh = 0;
let refreshingSounds = false;
async function refreshSounds() {
 if (refreshingSounds) return;
 refreshingSounds = true;
 try {const sounds=await request('/api/sounds');const key=JSON.stringify(sounds);if(key!==soundsKey){soundsKey=key;renderSounds(sounds);}lastSoundsRefresh=Date.now();}
 catch(error){feedback('sounds-feedback',error.message,true);}
 finally{refreshingSounds=false;}
}
let volumeDirty = false;
$('volume').addEventListener('input', () => {volumeDirty = true;$('volume-value').textContent = `${$('volume').value}%`;});
$('volume-form').addEventListener('submit', async event => {
  event.preventDefault();const button = event.submitter;button.disabled = true;
  try {const result = await request('/api/volume', {method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify({volume:Number($('volume').value)})});volumeDirty = false;feedback('volume-feedback', `${result.volume}% from the next notification.`);}
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
  const old = button.textContent;button.disabled = true;button.textContent = 'Preparing…';feedback(feedbackID, '');
  try {const result = await action();const saved=result.job.saved_sound;feedback(feedbackID, `Queued: ${result.job.label}.`+(saved?` Saved as ${saved.id}.`:""));if(saved)await refreshSounds();if(feedbackID==='url-feedback')$('save-url').checked=false;if(feedbackID==='upload-feedback')$('save-upload').checked=false;poll();}
  catch (error) {feedback(feedbackID, error.message, true);}
  finally {button.disabled = false;button.textContent = old;}
}
function renderSounds(sounds) {
  $('sound-list').replaceChildren();$('sound-count').textContent = `${sounds.length} sound${sounds.length===1?"":"s"}`;
  if (!sounds.length) {const p = document.createElement('p');p.className = 'empty';p.textContent = 'No local sounds found. Use a URL or upload a file.';$('sound-list').append(p);}
  for (const sound of sounds) {
    const row = document.createElement('div');row.className = 'sound';
    const info = document.createElement('div');info.className = 'sound-info';
    const name = document.createElement('strong');name.textContent = sound.id;
    const file = document.createElement('small');file.textContent = sound.filename;info.append(name, file);
    const button = document.createElement('button');button.type = 'button';button.textContent = '▶ Play';button.setAttribute('aria-label', `Play ${sound.id}`);
    button.addEventListener('click', () => submit(button, 'sounds-feedback', () => request(`/play/${encodeURIComponent(sound.id)}`, {method: 'POST'})));
    row.append(info, button);$('sound-list').append(row);
  }
}
$('url-form').addEventListener('submit', event => {event.preventDefault();const url = $('audio-url').value.trim();if (!/^https?:\/\//i.test(url)) {feedback('url-feedback', 'Enter an HTTP or HTTPS URL.', true);return;}submit(event.submitter, 'url-feedback', () => request('/play-url', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({url,save:$("save-url").checked})}));});
function selectedFile() {const file = $('audio-file').files[0];$('file-label').textContent = file ? file.name : 'Choose a file or drop it here';}
$('audio-file').addEventListener('change', selectedFile);
for (const name of ['dragenter', 'dragover']) $('drop').addEventListener(name, event => {event.preventDefault();$('drop').classList.add('dragging');});
for (const name of ['dragleave', 'drop']) $('drop').addEventListener(name, () => $('drop').classList.remove('dragging'));
$('drop').addEventListener('drop', event => {event.preventDefault();if (event.dataTransfer.files.length !== 1) {feedback('upload-feedback', 'Drop one file at a time.', true);return;}$('audio-file').files = event.dataTransfer.files;selectedFile();});
$('upload-form').addEventListener('submit', event => {event.preventDefault();const file = $('audio-file').files[0];if (!file) return;if (config && file.size > config.max_file_size_mb * 1024 * 1024) {feedback('upload-feedback', 'The file exceeds the size limit.', true);return;}const data = new FormData();data.append('file', file);if($('save-upload').checked)data.append('save','true');submit(event.submitter, 'upload-feedback', () => request('/play-file', {method: 'POST', body: data}));});
let lastHistory = '';
let polling = false;
let statusEpoch = 0;
function renderHistory(s) {
 $('last-error').hidden = !s.last_error;
 $('last-error').textContent = s.last_error ? `Last error: ${s.last_error}` : '';
 $('clear-history').disabled = !s.history.length && !s.last_error;
 const key = JSON.stringify(s.history);
 if (key === lastHistory) return;
 lastHistory = key;
 $('history').replaceChildren();
 if (!s.history.length) {const li = document.createElement('li');li.className='empty';li.textContent='No notifications yet.';$('history').append(li);return;}
 for (const e of s.history) {
  const li=document.createElement('li');li.className=`history-row ${e.state}`;
  const result=document.createElement('span');result.className='result';result.textContent=e.state==='completed'?'✓':'!';result.setAttribute('aria-label',e.state==='completed'?'Completed':'Failed');
  const details=document.createElement('div');details.className='details';
  const label=document.createElement('strong');label.textContent=e.label;details.append(label);
  if(e.error){const error=document.createElement('small');error.textContent=e.error;details.append(error);}
  const time=document.createElement('time');time.dateTime=e.at;time.textContent=new Date(e.at).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'});
  li.append(result,details,time);$('history').append(li);
 }
}
$('clear-history').addEventListener('click',async()=>{
 const button=$('clear-history');button.disabled=true;
 try {await request('/api/history',{method:'DELETE'});statusEpoch++;renderHistory({history:[],last_error:''});feedback('history-feedback','History cleared.');poll();}
 catch(error){feedback('history-feedback',error.message,true);button.disabled=false;}
});
async function poll() {
  if (polling) return;polling = true;const epoch = statusEpoch;
  try {
    const s = await request('/api/status');if(epoch!==statusEpoch)return;$('connection').textContent = 'Connected';$('connection').classList.remove('offline');
    if (!volumeDirty) {$('volume').value = s.volume;$('volume-value').textContent = `${s.volume}%`;}
    $('state').textContent = ({idle:'Idle', starting:'Starting', playing:'Playing', restoring:'Restoring'})[s.state] || s.state;
    $('current').textContent = s.current?.label || 'No playback';$('queued').textContent = s.queue;$('pulse').classList.toggle('active', s.state !== 'idle');
    $('mqtt').textContent = s.mqtt === 'disabled' ? 'MQTT disabled' : `MQTT: ${{connected:'connected', connecting:'connecting…', disconnected:'disconnected', 'subscription failed':'subscription failed'}[s.mqtt] || s.mqtt}`;
    renderHistory(s);
    if(Date.now()-lastSoundsRefresh>5000)refreshSounds();
  } catch { $('connection').textContent = 'Unavailable';$('connection').classList.add('offline'); }
  finally {polling = false;}
}
async function init() {
  try {const [sounds, settings] = await Promise.all([request('/api/sounds'), request('/api/config')]);config = settings;soundsKey=JSON.stringify(sounds);lastSoundsRefresh=Date.now();renderSounds(sounds);for(const id of ['save-url','save-upload']){$(id).disabled=settings.scan_directory===false;$(id).title=settings.scan_directory===false?'Enable directory scanning to save sounds.':'';}$('limits').textContent = `Up to ${settings.max_file_size_mb} MiB · ${settings.max_audio_duration_seconds} s audio`;$('version').textContent = `snapnotify ${settings.version}`;}
  catch (error) {feedback('sounds-feedback', 'Could not load settings. Reload the page.', true);}
  poll();setInterval(() => {if (!document.hidden) poll();}, 1000);document.addEventListener('visibilitychange', () => {if (!document.hidden) poll();});
}
init();
