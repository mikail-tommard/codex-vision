"use strict";
(() => {
  const $ = id => document.getElementById(id);
  const text = (element, value) => { if (element.textContent !== value) element.textContent = value; };
  let token = location.hash.slice(1), snapshot = null, receivedAt = 0, lastRevision = 0, notifications = false, notice = null, pip = null, stopped = false, toastTimer;
  try { if (!token) token = sessionStorage.getItem("vision-coach-token") || ""; if (token) sessionStorage.setItem("vision-coach-token", token); } catch (_) {}
  if (location.hash) history.replaceState(null, "", location.pathname + location.search);
  let theme = "auto";
  try { theme = localStorage.getItem("vision-coach-theme") || "auto"; } catch (_) {}
  const texts = {ready:"Актуально", waiting:"Ждём подсказку", paused:"Пауза", stopped:"Завершено", scene_changed:"Сцена изменилась", source_changed:"Область изменилась", source_not_visible:"Вернись к игре", expired:"Нужно обновление", observation_unavailable:"Нет наблюдения", disconnected:"Связь потеряна"};
  function toast(message) { $("toast").textContent = message; $("toast").hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => { $("toast").hidden = true; }, 5500); }
  function applyTheme() {
    if (theme === "auto") delete document.documentElement.dataset.theme; else document.documentElement.dataset.theme = theme;
    $("theme").title = "Тема: " + ({auto:"системная", dark:"тёмная", light:"светлая"}[theme] || "системная");
    $("theme").setAttribute("aria-label", $("theme").title + ". Сменить тему");
    if (pip && !pip.closed) { if (theme === "auto") delete pip.document.documentElement.dataset.theme; else pip.document.documentElement.dataset.theme = theme; }
  }
  applyTheme();
  $("theme").addEventListener("click", () => { theme = ({auto:"light",light:"dark",dark:"auto"})[theme] || "auto"; try { localStorage.setItem("vision-coach-theme",theme); } catch (_) {} applyTheme(); });
  function current() {
    if (!snapshot) return null;
    const s = {...snapshot}, a = s.advice;
    s.ageMS = a ? Math.max(0, Date.parse(s.server_time) - Date.parse(a.created_at) + performance.now() - receivedAt) : 0;
    if (!s.stale && a && s.ageMS >= a.ttl_seconds * 1000) { s.stale = true; s.status = "expired"; }
    return s;
  }
  function paint(doc, s) {
    const el = id => doc.getElementById(id), a = s && s.advice;
    if (!el("advice")) return;
    el("advice").classList.toggle("is-stale", !s || s.stale);
    text(el("state-label"), s ? texts[s.status] || "Нужно обновление" : "Подключение");
    el("recommendation").textContent = a ? a.recommendation : "Ждём первую подсказку";
    el("reason").textContent = a ? a.reason : "Выдели область игры и попроси Codex наблюдать за ней в режиме советника.";
    el("alternative-section").hidden = !(a && a.alternative);
    el("alternative").textContent = a ? a.alternative || "" : "";
    el("context-line").textContent = !s ? "Подключаемся к локальному советнику." : !s.active ? "Сеанс советника завершён." : s.stale && a ? "Прошлая рекомендация. Дождись анализа текущей ситуации." : a ? ({normal:"Рекомендация для текущей ситуации.", important:"Стоит обратить внимание.", critical:"Требует твоего внимания."}[a.urgency] || "Рекомендация для текущей ситуации.") : "Ты играешь — советник готовит второй взгляд.";
    el("updated").textContent = a ? "Обновлено " + Math.floor(s.ageMS / 1000) + " с назад" : "Ещё нет обновлений";
    if (a) el("updated").dateTime = a.created_at;
  }
  function render() {
    const s = current(); paint(document, s); if (pip && !pip.closed) paint(pip.document, s);
    if (!s || s.stale) { if (notice) { notice.close(); notice = null; } }
    if (!s) return;
    $("pause").disabled = !s.active; $("pause").textContent = s.paused ? "Продолжить" : "Пауза";
    $("session-label").textContent = s.paused ? "Подсказки на паузе" : s.active ? "Без управления игрой" : "Сеанс завершён";
    text($("connection"), s.status === "disconnected" ? "Нет связи с локальным сервером" : "Локальное соединение · без передачи экрана в панель");
  }
  async function request(path, options = {}) {
    const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 3000);
    try {
      const response = await fetch(path, {...options, cache:"no-store", signal:controller.signal, headers:{Authorization:"Bearer " + token, ...(options.headers || {})}});
      if (!response.ok) { const e = new Error("HTTP " + response.status); e.status = response.status; throw e; }
      return await response.json();
    } finally { clearTimeout(timeout); }
  }
  function accept(s) {
    snapshot = s; receivedAt = performance.now(); render();
    if (s.advice && s.advice.revision !== lastRevision) {
      lastRevision = s.advice.revision;
      if (notice) { notice.close(); notice = null; }
      if (!s.stale) {
        $("announcement").textContent = s.advice.recommendation;
        if (notifications && s.advice.urgency !== "normal" && "Notification" in window && Notification.permission === "granted") {
          try { notice = new Notification(s.advice.urgency === "critical" ? "Vision Coach · Важный момент" : "Vision Coach · Подсказка", {body:s.advice.recommendation, tag:"vision-coach-advice", silent:true}); } catch (_) { toast("Системные уведомления недоступны. Подсказки остаются в панели."); }
        }
      }
    }
  }
  async function poll() {
    if (stopped) return;
    if (token) {
      try { const s = await request("state"); $("pairing").hidden = true; accept(s); }
      catch (e) {
        if (e.status === 401) { token = ""; try { sessionStorage.removeItem("vision-coach-token"); } catch (_) {} $("pairing").hidden = false; }
        if (snapshot) { snapshot = {...snapshot, stale:true, status:"disconnected"}; render(); }
        else { $("connection").textContent = e.status === 401 ? "Нужен ключ из ссылки советника" : "Локальный сервер недоступен"; }
      }
    } else $("pairing").hidden = false;
    setTimeout(poll, document.hidden && !(pip && !pip.closed) ? 3000 : 1000);
  }
  $("pair-form").addEventListener("submit", e => { e.preventDefault(); const value = $("pair-token").value.trim(); if (!/^[a-f0-9]{64}$/.test(value)) { toast("Ключ должен содержать 64 символа из ссылки советника."); return; } token = value; $("pair-token").value = ""; try { sessionStorage.setItem("vision-coach-token",token); } catch (_) {} });
  $("pause").addEventListener("click", async () => { $("pause").disabled = true; try { accept(await request("control",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({action:snapshot.paused ? "resume" : "pause"})})); } catch (_) { toast("Не удалось изменить состояние. Проверь соединение."); render(); } });
  $("notifications").addEventListener("click", async () => {
    if (notifications) notifications = false;
    else if (!("Notification" in window) || !window.isSecureContext) { toast("В этом браузере уведомления недоступны. Используй плавающую панель."); return; }
    else { try { notifications = await Notification.requestPermission() === "granted"; } catch (_) { notifications = false; } if (!notifications) toast("Уведомления не разрешены. Подсказки остаются в панели."); }
    $("notifications").setAttribute("aria-checked", String(notifications));
    if (!notifications && notice) { notice.close(); notice = null; }
  });
  const isCompact = new URLSearchParams(location.search).get("compact") === "1";
  document.body.classList.toggle("compact",isCompact); $("compact").setAttribute("aria-pressed",String(isCompact)); $("expand").hidden=!isCompact;
  $("compact").addEventListener("click", () => { document.body.classList.add("compact"); $("compact").setAttribute("aria-pressed","true"); $("expand").hidden=false; $("expand").focus(); });
  $("expand").addEventListener("click", () => { document.body.classList.remove("compact"); $("compact").setAttribute("aria-pressed","false"); $("expand").hidden=true; $("compact").focus(); });
  $("float").addEventListener("click", async () => {
    if (pip && !pip.closed) { pip.focus(); return; }
    if ("documentPictureInPicture" in window && window.isSecureContext) {
      try {
        pip = await window.documentPictureInPicture.requestWindow({width:420,height:530});
        pip.document.title = "Vision Coach · Подсказка"; pip.document.documentElement.lang = "ru";
        const style = pip.document.createElement("link"); style.rel = "stylesheet"; style.href = new URL("style.css",location.href).href; pip.document.head.append(style);
        const main = pip.document.createElement("main"); main.append($("advice").cloneNode(true)); pip.document.body.className = "floating"; pip.document.body.append(main);
        pip.document.getElementById("announcement").remove(); applyTheme(); render(); return;
      } catch (_) { toast("Плавающий режим недоступен — открываем отдельное окно."); }
    }
    const url = new URL(location.href); url.searchParams.set("compact","1"); url.hash = token;
    const child = window.open(url.href,"vision-coach-panel","popup,width=440,height=650");
    if (!child) toast("Браузер заблокировал окно. Разреши всплывающие окна для этой страницы.");
    else toast("Открыто отдельное окно. Закрепление поверх игры зависит от браузера и ОС.");
  });
  if (!("documentPictureInPicture" in window)) $("floating-note").textContent = "Браузер откроет отдельное компактное окно. Автоматическое закрепление поверх игры здесь недоступно.";
  window.addEventListener("pagehide", () => { stopped = true; if (notice) notice.close(); });
  window.addEventListener("pageshow", e => { if (e.persisted) { stopped = false; poll(); } });
  setInterval(render,1000); poll();
})();
