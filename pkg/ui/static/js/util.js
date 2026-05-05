// util.js — shared helpers used by all page scripts.
'use strict';

// escHtml escapes a string for safe insertion into HTML text content.
function escHtml(s) {
  return String(s || '').replace(/&/g,'&amp;').replace(/</g,'&lt;')
                        .replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// fmtDate formats an ISO 8601 timestamp to a human-readable local string.
function fmtDate(iso) {
  if (!iso) return '—';
  return new Date(iso).toLocaleString(undefined, {
    year:'numeric', month:'2-digit', day:'2-digit',
    hour:'2-digit', minute:'2-digit', second:'2-digit',
  });
}

// duration returns a human-readable duration between two ISO timestamps.
// Returns '—' if end is absent (still running).
function duration(start, end) {
  if (!end) return '—';
  var ms = new Date(end) - new Date(start);
  if (ms < 1000)  return ms + 'ms';
  if (ms < 60000) return (ms / 1000).toFixed(1) + 's';
  return Math.floor(ms / 60000) + 'm ' + (Math.floor(ms / 1000) % 60) + 's';
}

// badgeClass returns the CSS class for a given state string.
function badgeClass(state) {
  var map = {
    RUNNING:'badge-running', PENDING:'badge-pending',
    SUCCEEDED:'badge-succeeded', FAILED:'badge-failed',
    CANCELLED:'badge-cancelled',
  };
  return map[state] || 'badge-unknown';
}

// badgeHtml returns a full <span class="badge …">STATE</span> string.
function badgeHtml(state) {
  return '<span class="badge ' + escHtml(badgeClass(state)) + '">'
       + escHtml(state || 'UNKNOWN') + '</span>';
}

// toast shows a brief notification at the bottom-right of the screen.
// Pass isErr=true to style it as an error.
function toast(msg, isErr) {
  var el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.className   = 'toast show' + (isErr ? ' error' : '');
  clearTimeout(el._tid);
  el._tid = setTimeout(function() { el.className = 'toast'; }, 3500);
}

// opUUID strips the "operations/" prefix from an LRO name.
function opUUID(name) { return String(name || '').replace(/^operations\//, ''); }

// analysisUUID strips the "analyses/" prefix from a resource name.
function analysisUUID(name) { return String(name || '').replace(/^analyses\//, ''); }

// analysisNameFromOp converts "operations/{uuid}" → "analyses/{uuid}".
function analysisNameFromOp(name) {
  return 'analyses/' + opUUID(name);
}

// opNameFromAnalysis converts "analyses/{uuid}" → "operations/{uuid}".
function opNameFromAnalysis(name) {
  return 'operations/' + analysisUUID(name);
}
