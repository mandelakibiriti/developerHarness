(function () {
  'use strict';
  var NS = 'http://www.w3.org/2000/svg';
  var H = window.H;

  var ACCENT = '#58a6ff';
  var MUTED = '#8b949e';
  var BORDER = '#30363d';
  var TEXT = '#e6edf3';
  var FONT = 'sans-serif';

  var uid = 0;

  function svgEl(tag, attrs) {
    var el = document.createElementNS(NS, tag);
    if (attrs) {
      for (var k in attrs) {
        if (Object.prototype.hasOwnProperty.call(attrs, k)) el.setAttribute(k, attrs[k]);
      }
    }
    return el;
  }

  function truncate(s, n) {
    if (s == null) return '';
    s = String(s);
    return s.length > n ? s.slice(0, n - 1) + '\u2026' : s;
  }

  function hint(container, msg) {
    container.appendChild(H.el('p', 'hint', msg));
  }

  function makeSvg(width, height) {
    return svgEl('svg', { width: width, height: height, viewBox: '0 0 ' + width + ' ' + height });
  }

  function addArrowMarker(defs) {
    var id = 'arrow-' + (++uid);
    var m = svgEl('marker', {
      id: id, markerWidth: 8, markerHeight: 8, refX: 6, refY: 4,
      orient: 'auto', markerUnits: 'strokeWidth'
    });
    m.appendChild(svgEl('path', { d: 'M0,0 L8,4 L0,8 z', fill: MUTED }));
    defs.appendChild(m);
    return 'url(#' + id + ')';
  }

  function renderFloorplan(containerEl, plan) {
    containerEl.innerHTML = '';
    try {
      plan = plan || {};
      var comps = Array.isArray(plan.components) ? plan.components.filter(Boolean) : [];

      if (plan.goal) {
        containerEl.appendChild(H.el('p', 'pane-title', plan.goal));
      }
      if (comps.length === 0) {
        hint(containerEl, 'No components in the blueprint.');
        return;
      }

      var buildOrder = Array.isArray(plan.build_order) ? plan.build_order : [];
      var byId = {};
      comps.forEach(function (c) { if (c.id != null) byId[c.id] = c; });

      var orderedIds = [];
      var seen = {};
      if (buildOrder.length > 0) {
        buildOrder.forEach(function (id) {
          if (byId[id] && !seen[id]) { seen[id] = true; orderedIds.push(id); }
        });
        comps.forEach(function (c) {
          if (c.id != null && !seen[c.id]) { seen[c.id] = true; orderedIds.push(c.id); }
        });
      } else {
        comps.forEach(function (c) { orderedIds.push(c.id); });
      }

      var BOX_W = 230, BOX_H = 60, GAP_X = 20, GAP_Y = 28, PAD = 20;
      var n = orderedIds.length;
      var cols = buildOrder.length > 0 ? 1 : 4;
      var rows = Math.ceil(n / cols);
      var pos = {};
      for (var i = 0; i < n; i++) {
        var id = orderedIds[i];
        var col = i % cols;
        var row = Math.floor(i / cols);
        pos[id] = { x: PAD + col * (BOX_W + GAP_X), y: PAD + row * (BOX_H + GAP_Y) };
      }

      var width = PAD * 2 + cols * BOX_W + (cols - 1) * GAP_X;
      var height = PAD * 2 + rows * BOX_H + (rows - 1) * GAP_Y;
      var svg = makeSvg(width, height);

      var defs = svgEl('defs', {});
      var markerRef = addArrowMarker(defs);
      svg.appendChild(defs);

      orderedIds.forEach(function (cid) {
        var c = byId[cid];
        var deps = Array.isArray(c.depends_on) ? c.depends_on : [];
        deps.forEach(function (depId) {
          if (!pos[cid] || !pos[depId]) return;
          svg.appendChild(svgEl('line', {
            x1: pos[cid].x + BOX_W / 2, y1: pos[cid].y,
            x2: pos[depId].x + BOX_W / 2, y2: pos[depId].y + BOX_H,
            stroke: MUTED, 'stroke-width': 1.5, 'marker-end': markerRef
          }));
        });
      });

      orderedIds.forEach(function (cid) {
        var c = byId[cid];
        var p = pos[cid];
        svg.appendChild(svgEl('rect', {
          x: p.x, y: p.y, width: BOX_W, height: BOX_H,
          rx: 8, ry: 8, fill: 'none', stroke: BORDER, 'stroke-width': 1
        }));
        var nameText = svgEl('text', {
          x: p.x + 10, y: p.y + 22, fill: ACCENT,
          'font-size': 13, 'font-family': FONT
        });
        nameText.textContent = truncate(c.name != null ? c.name : cid, 34);
        svg.appendChild(nameText);
        if (c.description) {
          var descText = svgEl('text', {
            x: p.x + 10, y: p.y + 42, fill: TEXT,
            'font-size': 11, 'font-family': FONT
          });
          descText.textContent = truncate(c.description, 60);
          svg.appendChild(descText);
        }
      });

      containerEl.appendChild(svg);

      if (buildOrder.length > 0) {
        var legend = buildOrder.map(function (bid) {
          var c = byId[bid];
          return c && c.name != null ? c.name : bid;
        }).join(', ');
        containerEl.appendChild(H.el('p', 'muted', 'Build order: ' + legend));
      }
    } catch (e) {
      containerEl.innerHTML = '';
      hint(containerEl, 'Failed to render floorplan: ' + e.message);
    }
  }

  function renderGraph(containerEl, graph) {
    containerEl.innerHTML = '';
    try {
      graph = graph || {};
      var nodes = Array.isArray(graph.nodes) ? graph.nodes.filter(Boolean) : [];
      var links = Array.isArray(graph.links) ? graph.links : [];

      if (nodes.length === 0) {
        hint(containerEl, 'No nodes.');
        return;
      }
      if (nodes.length > 200) {
        hint(containerEl, 'Graph has ' + nodes.length + ' nodes \u2014 too large to draw; use traversal.');
        return;
      }

      var cols = 8;
      var rows = Math.ceil(nodes.length / cols);
      var R = 16;
      var CELL_W = 180, CELL_H = 70, PAD = 24;
      var width = PAD * 2 + cols * CELL_W;
      var height = PAD * 2 + rows * CELL_H;

      var center = {};
      nodes.forEach(function (node, i) {
        var nid = node.id != null ? node.id : String(i);
        var col = i % cols;
        var row = Math.floor(i / cols);
        center[nid] = { x: PAD + col * CELL_W + R + 8, y: PAD + row * CELL_H + CELL_H / 2 };
      });

      var svg = makeSvg(width, height);

      var linkCount = 0;
      links.forEach(function (link) {
        if (!link || link.source == null || link.target == null) return;
        var s = center[link.source];
        var t = center[link.target];
        if (!s || !t) return;
        svg.appendChild(svgEl('line', {
          x1: s.x, y1: s.y, x2: t.x, y2: t.y,
          stroke: MUTED, 'stroke-width': 1, opacity: 0.6
        }));
        if (linkCount < 40 && link.relation) {
          var label = svgEl('text', {
            x: (s.x + t.x) / 2, y: (s.y + t.y) / 2 - 3,
            fill: MUTED, 'font-size': 9, 'font-family': FONT, 'text-anchor': 'middle'
          });
          label.textContent = truncate(link.relation, 20);
          svg.appendChild(label);
        }
        linkCount++;
      });

      nodes.forEach(function (node, i) {
        var nid = node.id != null ? node.id : String(i);
        var c = center[nid];
        svg.appendChild(svgEl('circle', {
          cx: c.x, cy: c.y, r: R, fill: 'none', stroke: ACCENT, 'stroke-width': 1.5
        }));
        var label = node.label != null ? node.label : (node.id != null ? node.id : String(i));
        var text = svgEl('text', {
          x: c.x + R + 6, y: c.y + 4, fill: TEXT,
          'font-size': 11, 'font-family': FONT
        });
        text.textContent = truncate(label, 28);
        svg.appendChild(text);
      });

      containerEl.appendChild(svg);
    } catch (e) {
      containerEl.innerHTML = '';
      hint(containerEl, 'Failed to render graph: ' + e.message);
    }
  }

  window.Visuals = {
    renderFloorplan: renderFloorplan,
    renderGraph: renderGraph
  };
})();
