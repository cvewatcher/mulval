// graph.js — D3 force-directed attack graph renderer.
// Requires d3 to be loaded before this script.
// Exports: renderAttackGraph(attackGraph)
'use strict';

var _gZoom       = null;
var _showLabels  = true;

// parseAttackGraph converts an AttackGraph proto object into {vertices, arcs}.
// Falls back to the parsed proto arrays if CSV strings are absent.
function parseAttackGraph(ag) {
  function parseCSV(csv) {
    var lines = (csv || '').trim().split('\n');
    return lines.slice(1).map(function(l) {
      var parts = [], cur = '', inQ = false;
      for (var i = 0; i < l.length; i++) {
        var ch = l[i];
        if (ch === '"' && !inQ) { inQ = true; }
        else if (ch === '"' && inQ) { inQ = false; }
        else if (ch === ',' && !inQ) { parts.push(cur); cur = ''; }
        else { cur += ch; }
      }
      parts.push(cur);
      return parts;
    }).filter(function(p) { return p.length >= 2 && p[0] !== '' && !isNaN(+p[0]); });
  }

  var vertices, arcs;

  // Prefer the parsed proto arrays (populated server-side from CSV).
  // Fall back to client-side CSV parsing only if proto arrays are absent.
  if (ag.vertices && ag.vertices.length > 0) {
    vertices = ag.vertices.map(function(v) {
      return {
        id:     String(v.id),
        label:  v.fact || '',
        type:   (v.vertexType || '').replace('VERTEX_TYPE_', ''),
        isGoal: !!v.isGoal,
      };
    });
  } else if (ag.verticesCsv) {
    // MulVAL VERTICES.CSV format (no header):  id,"fact","TYPE",isFact
    // Vertex 1 is always the attackGoal root.
    vertices = parseCSV(ag.verticesCsv).map(function(p) {
      var raw_type = (p[2] || '').replace(/"/g, '').trim().toUpperCase();
      return {
        id:     p[0],
        label:  (p[1] || '').replace(/"/g, ''),
        type:   raw_type,
        isGoal: p[0] === '1',
      };
    });
  }

  if (ag.arcs && ag.arcs.length > 0) {
    arcs = ag.arcs.map(function(a) {
      return { source: String(a.src), target: String(a.dst) };
    });
  } else if (ag.arcsCsv) {
    // MulVAL ARCS.CSV format (no header):  src,dst,weight  (weight always -1)
    arcs = parseCSV(ag.arcsCsv).map(function(p) {
      return { source: p[0], target: p[1] };
    });
  }

  return { vertices: vertices, arcs: arcs };
}

function _nodeColor(v) {
  if (v.isGoal)          return '#b91c1c';
  if (v.type === 'LEAF') return '#92400e';
  if (v.type === 'OR')   return '#1a6b3c';
  return '#1d4ed8'; // AND or unknown
}

// renderAttackGraph renders a force-directed graph into #graph-svg.
// ag is an analysisAttackGraph object from the API.
function renderAttackGraph(ag) {
  var parsed   = parseAttackGraph(ag);
  var vertices = parsed.vertices;
  var arcs     = parsed.arcs;

  var svgEl = document.getElementById('graph-svg');
  if (!svgEl) return;

  var svg = d3.select('#graph-svg');
  svg.selectAll('*').remove();

  var W = svgEl.clientWidth || 880, H = 420;
  var g = svg.append('g');

  _gZoom = d3.zoom().scaleExtent([.25, 5])
    .on('zoom', function(e) { g.attr('transform', e.transform); });
  svg.call(_gZoom);

  // Arrow marker
  svg.append('defs').append('marker')
    .attr('id', 'arrow')
    .attr('viewBox', '0 -5 10 10')
    .attr('refX', 18).attr('refY', 0)
    .attr('markerWidth', 6).attr('markerHeight', 6)
    .attr('orient', 'auto')
    .append('path').attr('d', 'M0,-5L10,0L0,5').attr('fill', '#94a3b8');

  var nodes = vertices.map(function(v) { return Object.assign({}, v); });
  var links = arcs.map(function(a) { return Object.assign({}, a); });

  var sim = d3.forceSimulation(nodes)
    .force('link',    d3.forceLink(links).id(function(d) { return d.id; }).distance(90).strength(1))
    .force('charge',  d3.forceManyBody().strength(-320))
    .force('center',  d3.forceCenter(W / 2, H / 2))
    .force('collide', d3.forceCollide(34));

  var link = g.append('g').selectAll('line').data(links).join('line')
    .attr('stroke', '#94a3b8')
    .attr('stroke-width', 1.5)
    .attr('marker-end', 'url(#arrow)');

  var node = g.append('g').selectAll('g').data(nodes).join('g')
    .attr('cursor', 'pointer')
    .call(
      d3.drag()
        .on('start', function(e, d) { if (!e.active) sim.alphaTarget(.3).restart(); d.fx = d.x; d.fy = d.y; })
        .on('drag',  function(e, d) { d.fx = e.x; d.fy = e.y; })
        .on('end',   function(e, d) { if (!e.active) sim.alphaTarget(0); d.fx = null; d.fy = null; })
    );

  node.each(function(d) {
    var s = d3.select(this);
    var c = _nodeColor(d);
    if (d.type === 'LEAF' || d.isGoal) {
      s.append('circle').attr('r', 12)
       .attr('fill', c).attr('opacity', .18)
       .attr('stroke', c).attr('stroke-width', 1.8);
    } else {
      s.append('rect').attr('x', -22).attr('y', -11).attr('width', 44).attr('height', 22)
       .attr('rx', 4)
       .attr('fill', c).attr('opacity', .15)
       .attr('stroke', c).attr('stroke-width', 1.8);
    }
  });

  node.append('text')
    .attr('class', 'g-label')
    .attr('text-anchor', 'middle')
    .attr('dy', 28)
    .attr('font-size', 9)
    .attr('font-family', 'IBM Plex Mono, monospace')
    .attr('fill', '#4a4a4a')
    .text(function(d) { return d.label.length > 30 ? d.label.slice(0, 29) + '…' : d.label; })
    .style('display', _showLabels ? '' : 'none');

  var tip = document.getElementById('node-tip');
  node
    .on('mouseenter', function(e, d) {
      if (!tip) return;
      tip.style.display = 'block';
      tip.textContent   = d.label + (d.isGoal ? ' ★ GOAL' : '');
      tip.style.left    = (e.offsetX + 14) + 'px';
      tip.style.top     = (e.offsetY - 8)  + 'px';
    })
    .on('mousemove', function(e) {
      if (!tip) return;
      tip.style.left = (e.offsetX + 14) + 'px';
      tip.style.top  = (e.offsetY - 8)  + 'px';
    })
    .on('mouseleave', function() { if (tip) tip.style.display = 'none'; });

  sim.on('tick', function() {
    link
      .attr('x1', function(d) { return d.source.x; })
      .attr('y1', function(d) { return d.source.y; })
      .attr('x2', function(d) { return d.target.x; })
      .attr('y2', function(d) { return d.target.y; });
    node.attr('transform', function(d) { return 'translate(' + d.x + ',' + d.y + ')'; });
  });
}

// resetZoom resets the SVG viewport to the identity transform.
function resetZoom() {
  if (!_gZoom) return;
  d3.select('#graph-svg').transition().duration(350)
    .call(_gZoom.transform, d3.zoomIdentity);
}

// toggleLabels shows/hides node labels and updates the button text.
function toggleLabels() {
  _showLabels = !_showLabels;
  var btn = document.getElementById('lbl-btn');
  if (btn) btn.textContent = 'Labels: ' + (_showLabels ? 'on' : 'off');
  d3.selectAll('.g-label').style('display', _showLabels ? '' : 'none');
}
