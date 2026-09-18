window.connection.read().then(config => {
  document.querySelector(`input[value="${config.mode === 'remote' ? 'remote' : 'local'}"]`).checked = true;
  document.getElementById('url').value = config.url || '';
  document.getElementById('allowHTTP').checked = config.allowHTTP === true;
}).catch(error => { document.getElementById('error').textContent = error.message; });
document.getElementById('connection-form').addEventListener('submit', async event => {
  event.preventDefault();
  try {
    await window.connection.save({ mode: document.querySelector('input[name="mode"]:checked').value,
      url: document.getElementById('url').value.trim(), allowHTTP: document.getElementById('allowHTTP').checked });
  } catch (error) { document.getElementById('error').textContent = error.message; }
});
