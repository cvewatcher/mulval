// api.js — typed fetch wrappers for the MulVAL gRPC-gateway REST API.
// All functions return Promises. Rejected promises carry an Error whose
// message contains the HTTP status code and the server error message.
'use strict';

var API_BASE = '/api/v1';
var WAIT_TIMEOUT = '55s'; // slightly below browser 60 s fetch timeout

// _fetch is the low-level helper used by all api.* functions.
function _fetch(method, path, body, signal) {
  var opts = { method: method, headers: { 'Content-Type': 'application/json' } };
  if (body)   opts.body   = JSON.stringify(body);
  if (signal) opts.signal = signal;
  return fetch(API_BASE + path, opts).then(function(res) {
    if (!res.ok) {
      return res.json().catch(function() { return {}; }).then(function(j) {
        throw new Error('[' + res.status + '] ' + (j.message || j.error || res.statusText));
      });
    }
    if (res.status === 204) return {};
    return res.json();
  });
}

var api = {
  // ── Analyses ──────────────────────────────────────────────────────────────

  // listAnalyses returns { analyses: [...], nextPageToken: '' }.
  listAnalyses: function(pageToken) {
    var qs = '?pageSize=100' + (pageToken ? '&pageToken=' + encodeURIComponent(pageToken) : '');
    return _fetch('GET', '/analyses' + qs);
  },

  // getAnalysis returns an analysisAnalysis object.
  // name must be in the form "analyses/{uuid}".
  getAnalysis: function(name) {
    return _fetch('GET', '/' + name);
  },

  // createAnalysis submits a new analysis and returns a longrunningOperation.
  createAnalysis: function(edbFacts, idbRules, analysisId) {
    var body = { edbFacts: edbFacts, idbRules: idbRules };
    if (analysisId) body.analysisId = analysisId;
    return _fetch('POST', '/analyses', body);
  },

  // ── Operations ────────────────────────────────────────────────────────────

  // getOperation returns a googleLongrunningOperation.
  // name must be in the form "operations/{uuid}".
  // The gateway route is /api/v1/operations/{name} where {name} = "operations/{uuid}",
  // so we pass the full name (no prefix stripping).
  getOperation: function(name) {
    return _fetch('GET', '/operations/' + encodeURIComponent(name));
  },

  // waitOperation blocks server-side for up to WAIT_TIMEOUT.
  // Returns done=false if timeout elapses before completion.
  // Pass an AbortSignal to cancel the call on navigation.
  waitOperation: function(name, signal) {
    return _fetch(
      'POST',
      '/operations/' + encodeURIComponent(name) + ':wait',
      { timeout: WAIT_TIMEOUT },
      signal
    );
  },

  // cancelOperation requests best-effort cancellation.
  cancelOperation: function(name) {
    return _fetch(
      'POST',
      '/operations/' + encodeURIComponent(name) + ':cancel',
      {}
    );
  },
};
