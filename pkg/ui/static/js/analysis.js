// analysis.js — logic for the analysis detail page (/ui/a/{name}).
// Requires: util.js, api.js, graph.js
// The analysis name and operation name are injected by the Go template
// as window.ANALYSIS_NAME and window.OP_NAME.
'use strict';

(function () {
  var analysisName = window.ANALYSIS_NAME || '';
  var opName       = window.OP_NAME       || '';
  var waitAbort    = null;

  // ── Tab switching ──────────────────────────────────────────────────────────
  window.switchTab = function(group, tab, btn) {
    var map = {
      inputs: { edb: 'tab-edb', idb: 'tab-idb' },
      raw:    { vertices: 'tab-vertices', arcs: 'tab-arcs' },
    };
    var ids = map[group];
    if (!ids) return;
    var container = btn.closest('.tabs');
    container.querySelectorAll('.tab').forEach(function(t) { t.classList.remove('active'); });
    btn.classList.add('active');
    Object.keys(ids).forEach(function(k) {
      var el = document.getElementById(ids[k]);
      if (el) el.style.display = k === tab ? '' : 'none';
    });
  };

  // ── Helpers ────────────────────────────────────────────────────────────────
  function show(id) { var el = document.getElementById(id); if (el) el.style.display = ''; }
  function hide(id) { var el = document.getElementById(id); if (el) el.style.display = 'none'; }

  function kv(k, v) {
    return '<span class="kv-k">' + escHtml(k) + '</span>'
         + '<span class="kv-v">' + v + '</span>';
  }

  // ── Render: metadata ───────────────────────────────────────────────────────
  function renderMeta(a) {
    var el = document.getElementById('d-meta');
    if (!el) return;
    el.innerHTML =
      kv('State',     badgeHtml(a.state)) +
      kv('Analysis',  '<span class="mono-xs">' + escHtml(analysisUUID(a.name)) + '</span>') +
      kv('Operation', '<span class="mono-xs">' + escHtml(opName) + '</span>') +
      kv('Created',   escHtml(fmtDate(a.createTime))) +
      kv('Ended',     a.endTime ? escHtml(fmtDate(a.endTime)) : '—') +
      kv('Duration',  escHtml(duration(a.createTime, a.endTime)));
  }

  // ── Render: inputs ─────────────────────────────────────────────────────────
  function renderInputs(a) {
    var edbEl = document.getElementById('tab-edb');
    var idbEl = document.getElementById('tab-idb');
    if (edbEl) edbEl.textContent = (a.edbFacts || []).join('\n') || '(empty)';
    if (idbEl) idbEl.textContent = (a.idbRules || []).length
      ? a.idbRules.join('\n')
      : '% No custom rules — using MulVAL defaults.';
  }

  // ── Render: state sections ─────────────────────────────────────────────────
  function renderState(a) {
    hide('section-running');
    hide('section-failed');
    hide('section-succeeded');

    if (a.state === 'RUNNING' || a.state === 'PENDING') {
      show('section-running');
      return;
    }

    if (a.state === 'FAILED') {
      show('section-failed');
      var errEl = document.getElementById('d-error');
      if (errEl) errEl.textContent = a.error || 'Unknown error.';
      return;
    }

    if (a.state === 'SUCCEEDED') {
      show('section-succeeded');
      var ag = a.attackGraph;
      var hasGraph = ag && (
        (ag.verticesCsv && ag.verticesCsv.trim() !== '') ||
        (ag.vertices && ag.vertices.length > 0)
      );
      if (hasGraph) {
        renderGraphSection(ag);
      } else {
        renderEmptyGraph(ag);
      }
      return;
    }
    // CANCELLED — badge in metadata conveys state, no extra section needed.
  }

  // ── Render: attack graph section (paths found) ─────────────────────────────
  function renderGraphSection(ag) {
    // Count from proto arrays if populated; otherwise count lines in the CSV
    // (subtract 1 for the trailing newline MulVAL appends).
    var vertices, arcs, goals, leaves;
    if (ag.vertices && ag.vertices.length > 0) {
      vertices = ag.vertices.length;
      arcs     = (ag.arcs || []).length;
      goals    = 0;
      leaves   = 0;
      ag.vertices.forEach(function(v) {
        if (v.isGoal) goals++;
        if ((v.vertexType || '').replace('VERTEX_TYPE_','').indexOf('LEAF') !== -1) leaves++;
      });
    } else {
      // Fall back to counting CSV rows (no header row in MulVAL output).
      var vlines = (ag.verticesCsv || '').trim().split('\n').filter(Boolean);
      var alines = (ag.arcsCsv     || '').trim().split('\n').filter(Boolean);
      vertices = vlines.length;
      arcs     = alines.length;
      goals    = 1; // vertex 1 is always the goal
      leaves   = vlines.filter(function(l) { return l.indexOf('"LEAF"') !== -1 || l.indexOf(',LEAF,') !== -1; }).length;
    }

    var statsEl = document.getElementById('d-gstats');
    if (statsEl) {
      statsEl.innerHTML =
        kv('Vertices',   String(vertices)) +
        kv('Arcs',       String(arcs))     +
        kv('Goal nodes', String(goals))    +
        kv('Leaf nodes', String(leaves));
    }

    var summaryEl = document.getElementById('d-summary');
    if (summaryEl) summaryEl.textContent = ag.summary || '';

    var vEl = document.getElementById('tab-vertices');
    var aEl = document.getElementById('tab-arcs');
    if (vEl) vEl.textContent = ag.verticesCsv || '';
    if (aEl) aEl.textContent = ag.arcsCsv     || '';

    renderAttackGraph(ag);
  }

  // ── Render: no attack paths found ─────────────────────────────────────────
  function renderEmptyGraph(ag) {
    var statsEl = document.getElementById('d-gstats');
    if (statsEl) {
      statsEl.innerHTML =
        kv('Vertices',   '0') +
        kv('Arcs',       '0') +
        kv('Goal nodes', '0') +
        kv('Leaf nodes', '0');
    }

    var summaryEl = document.getElementById('d-summary');
    if (summaryEl) {
      summaryEl.textContent = (ag && ag.summary) ? ag.summary : 'No attack paths found.';
    }

    var svgEl = document.getElementById('graph-svg');
    if (svgEl) {
      var svg = d3.select('#graph-svg');
      svg.selectAll('*').remove();
      svg.append('text')
        .attr('x', svgEl.clientWidth  / 2 || 440)
        .attr('y', svgEl.clientHeight / 2 || 210)
        .attr('text-anchor', 'middle')
        .attr('dominant-baseline', 'middle')
        .attr('font-family', 'IBM Plex Sans, sans-serif')
        .attr('font-size', '14')
        .attr('fill', '#8a8a8a')
        .text('No attack paths found — goal unreachable from attacker position.');
    }

    var vEl = document.getElementById('tab-vertices');
    var aEl = document.getElementById('tab-arcs');
    if (vEl) vEl.textContent = '(empty — no attack paths)';
    if (aEl) aEl.textContent = '(empty — no attack paths)';
  }

  // ── Render: full page ──────────────────────────────────────────────────────
  function renderAnalysis(a) {
    renderMeta(a);
    renderInputs(a);
    var badgeEl = document.getElementById('d-badge');
    if (badgeEl) badgeEl.innerHTML = badgeHtml(a.state);
    renderState(a);
  }

  // ── WaitOperation loop ─────────────────────────────────────────────────────
  function startWaitLoop() {
    if (waitAbort) waitAbort.abort();
    waitAbort = new AbortController();
    var signal = waitAbort.signal;
    var msgEl  = document.getElementById('d-wait-msg');

    function loop() {
      if (signal.aborted) return;
      if (msgEl) msgEl.textContent = 'Waiting for MulVAL to complete (server-side block)…';

      api.waitOperation(opName, signal)
        .then(function(op) {
          if (signal.aborted) return;
          if (!op.done) { loop(); return; }
          return api.getAnalysis(analysisName).then(renderAnalysis);
        })
        .catch(function(e) {
          if (signal.aborted) return;
          if (msgEl) msgEl.textContent = 'Connection error, retrying… (' + e.message + ')';
          setTimeout(loop, 3000);
        });
    }

    loop();
  }

  // ── Cancel ─────────────────────────────────────────────────────────────────
  window.cancelAnalysis = function() {
    if (!confirm('Cancel this analysis?')) return;
    api.cancelOperation(opName)
      .then(function() {
        toast('Cancellation requested.');
        if (waitAbort) { waitAbort.abort(); waitAbort = null; }
        return api.getAnalysis(analysisName);
      })
      .then(renderAnalysis)
      .catch(function(e) { toast('Cancel failed: ' + e.message, true); });
  };

  // ── Init ───────────────────────────────────────────────────────────────────
  api.getAnalysis(analysisName)
    .then(function(a) {
      renderAnalysis(a);
      if (a.state === 'RUNNING' || a.state === 'PENDING') {
        startWaitLoop();
      }
    })
    .catch(function(e) {
      toast('Failed to load analysis: ' + e.message, true);
    });

}());
