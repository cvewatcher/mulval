// analyses.js — logic for the analyses list page (/ui/).
// Requires: util.js, api.js
'use strict';

(function () {
  // Load and render the analyses list.
  function loadAnalyses() {
    var btn = document.getElementById('refresh-btn');
    if (btn) btn.disabled = true;

    api.listAnalyses()
      .then(function(data) { renderTable(data.analyses || []); })
      .catch(function(e)   { toast('Failed to load analyses: ' + e.message, true); })
      .finally(function()  { if (btn) btn.disabled = false; });
  }

  function renderTable(analyses) {
    // Update stat counters.
    var counts = { total: 0, run: 0, ok: 0, fail: 0 };
    analyses.forEach(function(a) {
      counts.total++;
      if (a.state === 'RUNNING' || a.state === 'PENDING') counts.run++;
      if (a.state === 'SUCCEEDED') counts.ok++;
      if (a.state === 'FAILED')    counts.fail++;
    });
    ['total','run','ok','fail'].forEach(function(k) {
      var el = document.getElementById('cnt-' + k);
      if (el) el.textContent = counts[k];
    });

    var body = document.getElementById('analyses-tbody');
    if (!body) return;

    if (!analyses.length) {
      body.innerHTML =
        '<tr><td colspan="5"><div class="empty">' +
        '<div class="empty-icon">🔬</div>' +
        '<div class="empty-label">No analyses yet</div>' +
        '<div class="empty-sub">Create your first analysis to get started.</div>' +
        '</div></td></tr>';
      return;
    }

    body.innerHTML = analyses.map(function(a) {
      var uuid  = analysisUUID(a.name);
      return '<tr class="row-link" onclick="window.location=\'/ui/a/' + escHtml(a.name) + '\'">' +
        '<td>' + badgeHtml(a.state) + '</td>' +
        '<td><span class="mono-xs">' + escHtml(uuid) + '</span></td>' +
        '<td><span class="mono-xs">' + escHtml(fmtDate(a.createTime))  + '</span></td>' +
        '<td><span class="mono-xs">' + escHtml(duration(a.createTime, a.endTime)) + '</span></td>' +
        '</tr>';
    }).join('');
  }

  // Wire refresh button.
  var btn = document.getElementById('refresh-btn');
  if (btn) btn.addEventListener('click', loadAnalyses);

  loadAnalyses();
}());
