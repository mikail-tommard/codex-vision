const state = document.querySelector('#state');
const pause = document.querySelector('#pause');
const stop = document.querySelector('#stop');
const notice = document.querySelector('#notice');
const image = document.querySelector('img');
const labels = {running:'В эфире', paused:'На паузе', stopped:'Остановлено', error:'Ошибка'};
let current='connecting', ended=false, busy=false, revision=0;
let statusTimer, streamTimer, streamController, blobURL;
let statusFailures=0, streamFailures=0, streamLive=false, lastStatus;
const backoff = failures => Math.min(10000, 500 * 2 ** Math.min(failures, 5));

function finish(message) {
  ended=true;
  clearTimeout(statusTimer); clearTimeout(streamTimer);
  streamController?.abort();
  pause.disabled=stop.disabled=true;
  image.removeAttribute('src');
  if(blobURL) URL.revokeObjectURL(blobURL);
  notice.textContent=message;
}
function render(status) {
  lastStatus=status; current=status.state;
  state.textContent=labels[current] || current;
  document.querySelector('#mode').textContent=
    (status.mode==='task'?'На время задачи':'Постоянное наблюдение')+' · '+status.backend;
  document.querySelector('#stats').textContent=status.local_frames+' локальных кадров · '+
    status.images_returned+' кадров выдано агенту · интервал '+status.current_interval_ms+' мс';
  pause.textContent=current==='paused'?'Продолжить':'Пауза';
  pause.disabled=busy || !['running','paused'].includes(current);
  stop.disabled=busy || !['running','paused'].includes(current);
  notice.textContent=status.error || (current==='paused'?'Захват приостановлен.':
    !streamLive?'Восстанавливаю трансляцию. Последний кадр может быть устаревшим.':
    status.backend.startsWith('mock')?'Тестовый источник: это не экран вашего компьютера.':'');
  if(['stopped','error'].includes(current)) finish(status.error || 'Наблюдение завершено.');
}
async function refresh() {
  if(ended) return;
  const requestedRevision=revision;
  try {
    const response=await fetch('status',{cache:'no-store',signal:AbortSignal.timeout(4000)});
    if([403,404,410].includes(response.status)) {
      state.textContent='Сессия завершена'; finish('Эта сессия больше недоступна. Запустите наблюдение и откройте новую ссылку.'); return;
    }
    if(!response.ok) throw new Error('status unavailable');
    const status=await response.json(); statusFailures=0;
    if(!ended && requestedRevision===revision) render(status);
  } catch {
    if(!ended && requestedRevision===revision) {
      statusFailures++;
      state.textContent='Переподключение…';
      notice.textContent='Соединение прервано. Повторяю автоматически; последний кадр может быть устаревшим.';
    }
  }
  if(!ended) statusTimer=setTimeout(refresh,statusFailures?backoff(statusFailures):1500);
}

// Read multipart JPEG explicitly so EOF, failed requests and stalled connections
// are observable; an <img src=stream> cannot reliably report all these failures.
async function stream() {
  if(ended) return;
  streamController=new AbortController();
  const controller=streamController;
  let connectionTimer=setTimeout(()=>controller.abort(),5000);
  let reader;
  try {
    const response=await fetch('stream',{cache:'no-store',signal:controller.signal});
    clearTimeout(connectionTimer);
    if(!response.ok || !response.headers.get('Content-Type')?.startsWith('multipart/x-mixed-replace')) throw new Error('invalid stream');
    reader=response.body.getReader();
    let pending=new Uint8Array(0), length=null;
    while(!ended) {
      // Paused captures legitimately produce no bytes. Active streams receive
      // a deadline wider than the slowest configured sampling interval.
      const deadline=current==='paused'?null:setTimeout(()=>controller.abort(),40000);
      let chunk;
      try {chunk=await reader.read();} finally {if(deadline) clearTimeout(deadline);}
      if(chunk.done) throw new Error('stream ended');
      if(pending.length+chunk.value.length>32*1024*1024) throw new Error('frame too large');
      const combined=new Uint8Array(pending.length+chunk.value.length);
      combined.set(pending);combined.set(chunk.value,pending.length);pending=combined;
      while(true) {
        if(length===null) {
          let end=-1;
          for(let i=0;i+3<Math.min(pending.length,1024);i++) {
            if(pending[i]===13&&pending[i+1]===10&&pending[i+2]===13&&pending[i+3]===10){end=i;break;}
          }
          if(end<0){if(pending.length>1024)throw new Error('invalid frame header');break;}
          const header=new TextDecoder().decode(pending.subarray(0,end));
          const match=header.match(/Content-Length: (\d+)/i);
          if(!match || !/Content-Type: image\/jpeg/i.test(header)) throw new Error('invalid frame');
          length=Number(match[1]);if(length<1||length>32*1024*1024)throw new Error('invalid frame length');
          pending=pending.slice(end+4);
        }
        if(pending.length<length+2) break;
        const next=URL.createObjectURL(new Blob([pending.slice(0,length)],{type:'image/jpeg'}));
        const previous=blobURL;blobURL=next;image.src=next;
        if(previous)URL.revokeObjectURL(previous);
        pending=pending.slice(length+2);length=null;
        streamLive=true;streamFailures=0;
        if(lastStatus)render(lastStatus);
      }
    }
  } catch {
    if(!ended) {
      streamLive=false;streamFailures++;
      if(lastStatus)render(lastStatus);
    }
  } finally {
    clearTimeout(connectionTimer);
    controller.abort();
    if(reader){try{await reader.cancel();}catch{}}
    if(!ended)streamTimer=setTimeout(stream,backoff(streamFailures));
  }
}
async function control(action) {
  if(busy||ended) return;
  busy=true;revision++;pause.disabled=stop.disabled=true;
  try {
    const response=await fetch('control',{method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({action}),signal:AbortSignal.timeout(5000)});
    if(!response.ok)throw new Error('control failed');
    const status=await response.json();
    if(ended)return;
    if(action==='stop'){state.textContent='Остановлено';finish('Трансляция завершена.');}
    else{busy=false;render(status);}
  } catch {
    notice.textContent='Не удалось подтвердить действие. Состояние проверяется автоматически.';
  } finally {
    busy=false;
    if(!ended&&lastStatus){pause.disabled=stop.disabled=!['running','paused'].includes(lastStatus.state);}
  }
}
pause.addEventListener('click',()=>control(current==='paused'?'resume':'pause'));
stop.addEventListener('click',()=>control('stop'));
window.addEventListener('pagehide',()=>finish('Просмотр закрыт.'));
window.addEventListener('pageshow',event=>{if(event.persisted){ended=false;statusFailures=streamFailures=0;refresh();stream();}});
refresh();stream();
