(() => {
  const content = document.getElementById('live-content');
  const status = document.getElementById('live-status');
  const checked = document.getElementById('last-checked');
  let lastMessage = Date.now();
  const localize = () => document.querySelectorAll('time[datetime]').forEach(time => {
    time.textContent = new Date(time.dateTime).toLocaleString('cs-CZ');
  });
  const stale = () => {
    status.textContent = 'Přehled není aktuální. Obnovuji živé spojení…';
    status.dataset.state = 'stale';
  };
  localize();
  if (!window.EventSource) {
    status.textContent = 'Prohlížeč nepodporuje živé aktualizace. Použijte Obnovit přehled.';
    return;
  }
  let events;
  const receive = event => {
    try {
      const data = JSON.parse(event.data);
      if (data.html !== undefined) {
        content.innerHTML = data.html;
        localize();
      }
      checked.textContent = new Date(data.checkedAt).toLocaleString('cs-CZ');
      lastMessage = Date.now();
      status.textContent = 'Živé připojení k laboratoři';
      status.dataset.state = 'live';
    } catch {
      stale();
    }
  };
  const connect = () => {
    events?.close();
    lastMessage = Date.now();
    events = new EventSource('/events');
    events.addEventListener('snapshot', receive);
    events.addEventListener('heartbeat', receive);
    events.onerror = stale;
  };
  window.addEventListener('offline', () => { events?.close(); stale(); });
  window.addEventListener('online', connect);
  if (navigator.onLine) connect(); else stale();
  setInterval(() => {
    if (Date.now() - lastMessage > 20000) {
      stale();
      if (navigator.onLine) connect();
    }
  }, 5000);
})();
