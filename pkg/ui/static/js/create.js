// create.js — logic for the new analysis form (/ui/create).
// Requires: util.js, api.js
'use strict';

(function () {
  var form    = document.getElementById('create-form');
  var errEl   = document.getElementById('create-err');
  var submitBtn = document.getElementById('f-submit');

  function showErr(msg) {
    if (!errEl) return;
    errEl.textContent = msg;
    errEl.style.display = 'block';
  }
  function hideErr() {
    if (errEl) errEl.style.display = 'none';
  }

  window.clearForm = function() {
    ['f-edb','f-idb','f-id'].forEach(function(id) {
      var el = document.getElementById(id);
      if (el) el.value = '';
    });
    hideErr();
  };

  function submit(e) {
    if (e) e.preventDefault();
    hideErr();

    var edbRaw = (document.getElementById('f-edb') || {}).value || '';
    edbRaw = edbRaw.trim();
    if (!edbRaw) { showErr('EDB facts are required.'); return; }

    var edb = edbRaw.split('\n')
      .map(function(l) { return l.trim(); })
      .filter(function(l) { return l && l[0] !== '%'; });

    var idbRaw = ((document.getElementById('f-idb') || {}).value || '').trim();
    var idb = idbRaw
      ? idbRaw.split('\n').map(function(l) { return l.trim(); }).filter(Boolean)
      : [];

    var analysisId = ((document.getElementById('f-id') || {}).value || '').trim() || undefined;

    submitBtn.disabled = true;
    submitBtn.innerHTML = '<span class="spinner"></span>Submitting…';

    api.createAnalysis(edb, idb, analysisId)
      .then(function(op) {
        // op.name is "operations/{uuid}" — navigate to the analysis detail page.
        window.location = '/ui/a/analyses/' + opUUID(op.name);
      })
      .catch(function(e) {
        showErr(e.message);
        submitBtn.disabled = false;
        submitBtn.textContent = 'Run Analysis';
      });
  }

  if (form) form.addEventListener('submit', submit);
}());
