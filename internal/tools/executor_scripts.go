package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

func buildDOMSearchHelpers() string {
	return `function findFirstDeep(selector, root) {
  if (!selector || !selector.trim()) return root || document.body;
  root = root || document;
  if (root.querySelector) {
    var direct = root.querySelector(selector);
    if (direct) return direct;
  }
  var queue = [];
  if (root.shadowRoot) queue.push(root.shadowRoot);
  var seed = root.children || root.childNodes || [];
  for (var i = 0; i < seed.length; i++) queue.push(seed[i]);
  while (queue.length) {
    var node = queue.shift();
    if (!node) continue;
    if (node.nodeType === 1 && node.matches && node.matches(selector)) return node;
    if (node.shadowRoot) {
      if (node.shadowRoot.querySelector) {
        var shadowDirect = node.shadowRoot.querySelector(selector);
        if (shadowDirect) return shadowDirect;
      }
      queue.push(node.shadowRoot);
    }
    var children = node.children || node.childNodes || [];
    for (var j = 0; j < children.length; j++) queue.push(children[j]);
  }
  return null;
}
function collectAllDeep(root) {
  var out = [];
  var queue = [root || document.body];
  while (queue.length) {
    var node = queue.shift();
    if (!node) continue;
    if (node instanceof Element) out.push(node);
    if (node.shadowRoot) queue.push(node.shadowRoot);
    var children = node.children || node.childNodes || [];
    for (var i = 0; i < children.length; i++) queue.push(children[i]);
  }
  return out;
}
function collectDeepText(root, limit) {
  var parts = [];
  var nodes = collectAllDeep(root || document.body);
  for (var i = 0; i < nodes.length; i++) {
    var node = nodes[i];
    if (!(node instanceof Element)) continue;
    var txt = (node.innerText || node.textContent || '').replace(/\s+/g, ' ').trim();
    if (!txt) continue;
    parts.push(txt);
    if (parts.join(' ').length > limit) break;
  }
  var merged = parts.join(' ').replace(/\s+/g, ' ').trim();
  if (merged.length > limit) return merged.slice(0, limit);
  return merged;
}
function normalizeRefText(raw) {
  return (raw || '').replace(/\s+/g, ' ').trim().toLowerCase();
}
function roleOfRefNode(el) {
  if (!el || !el.tagName) return '';
  var explicitRole = el.getAttribute && el.getAttribute('role');
  if (explicitRole) return normalizeRefText(explicitRole);
  var tag = el.tagName.toLowerCase();
  if (tag === 'a' && el.getAttribute && el.getAttribute('href')) return 'link';
  if (tag === 'button') return 'button';
  if (tag === 'textarea') return 'textbox';
  if (tag === 'select') return 'combobox';
  if (tag === 'option') return 'option';
  if (tag === 'img') return 'img';
  if (tag === 'main') return 'main';
  if (tag === 'nav') return 'navigation';
  if (tag === 'header') return 'banner';
  if (tag === 'footer') return 'contentinfo';
  if (tag === 'form') return 'form';
  if (tag === 'dialog') return 'dialog';
  if (/^h[1-6]$/.test(tag)) return 'heading';
  if (tag === 'input') {
    var type = (el.getAttribute('type') || 'text').toLowerCase();
    if (type === 'button' || type === 'submit' || type === 'reset') return 'button';
    if (type === 'checkbox') return 'checkbox';
    if (type === 'radio') return 'radio';
    if (type === 'range') return 'slider';
    return 'textbox';
  }
  return tag;
}
function nameOfRefNode(el) {
  if (!el) return '';
  if (el.getAttribute) {
    var aria = el.getAttribute('aria-label');
    if (aria) return normalizeRefText(aria);
    var labelled = el.getAttribute('aria-labelledby');
    if (labelled) {
      var labelledName = labelled.split(/\s+/).map(function(id) {
        var node = document.getElementById(id);
        return node ? (node.innerText || node.textContent || '') : '';
      }).join(' ');
      if (labelledName.trim()) return normalizeRefText(labelledName);
    }
    var alt = el.getAttribute('alt');
    if (alt) return normalizeRefText(alt);
    var placeholder = el.getAttribute('placeholder');
    if (placeholder) return normalizeRefText(placeholder);
  }
  if ('value' in el && typeof el.value === 'string' && el.value.trim()) {
    return normalizeRefText(el.value);
  }
  return normalizeRefText(el.innerText || el.textContent || '');
}
function isVisibleRefNode(el) {
  if (!el || !(el instanceof Element)) return false;
  var style = window.getComputedStyle(el);
  if (!style) return true;
  if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false;
  return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length || (el.shadowRoot && el.shadowRoot.childNodes.length));
}
function resolveRefElement(ref) {
  var refs = window.__ariaRefs || {};
  var cached = refs[ref];
  if (cached && (!('isConnected' in cached) || cached.isConnected)) return cached;

  var metaRoot = window.__ariaRefMeta || {};
  var meta = metaRoot[ref];
  if (!meta) return null;
  if (metaRoot.__url && metaRoot.__url !== location.href) return null;

  var expectedRole = normalizeRefText(meta.role || '');
  var expectedName = normalizeRefText(meta.name || '');
  var expectedNth = (typeof meta.nth === 'number' && meta.nth >= 0) ? meta.nth : 0;

  var matches = [];
  var nodes = collectAllDeep(document.body);
  for (var i = 0; i < nodes.length; i++) {
    var el = nodes[i];
    if (!(el instanceof Element)) continue;
    if (!isVisibleRefNode(el)) continue;
    if (expectedRole && roleOfRefNode(el) !== expectedRole) continue;
    if (expectedName && nameOfRefNode(el) !== expectedName) continue;
    matches.push(el);
  }
  if (!matches.length) return null;

  var idx = expectedNth < matches.length ? expectedNth : 0;
  var found = matches[idx];
  refs[ref] = found;
  window.__ariaRefs = refs;
  return found || null;
}`
}

func buildGetTextExpression(selector string, maxChars int) string {
	selectorJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(function(){
  %s
  var sel = %s;
  var limit = %d;
  function pickText(el) {
    if (!el) return '';
    var raw = collectDeepText(el, limit);
    if (raw.length > limit) return raw.slice(0, limit);
    return raw;
  }
  if (sel && sel.trim()) return pickText(findFirstDeep(sel, document));
  return pickText(document.body);
})()`, buildDOMSearchHelpers(), string(selectorJSON), maxChars)
}

func buildSelectorStateExpression(selector string, state string) string {
	selJSON, _ := json.Marshal(selector)
	stateJSON, _ := json.Marshal(strings.ToLower(state))
	return fmt.Sprintf(`(function(){
  %s
  var sel = %s;
  var state = %s;
  var el = findFirstDeep(sel, document);
  if (state === 'attached') return !!el;
  if (state === 'detached') return !el;
  if (!el) return false;
  var style = window.getComputedStyle(el);
  var visible = !(style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') && !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
  if (state === 'visible') return visible;
  if (state === 'hidden') return !visible;
  return false;
})()`, buildDOMSearchHelpers(), string(selJSON), string(stateJSON))
}

func buildWaitTextExpression(text string, exact bool) string {
	textJSON, _ := json.Marshal(text)
	exactJSON, _ := json.Marshal(exact)
	return fmt.Sprintf(`(function(){
  %s
  var text = %s;
  var exact = %s;
  var body = collectDeepText(document.body, 200000);
  if (exact) return body === text;
  return body.indexOf(text) >= 0;
})()`, buildDOMSearchHelpers(), string(textJSON), string(exactJSON))
}

func buildWaitFunctionExpression(fn string) string {
	fnExpr := strings.TrimSpace(fn)
	return fmt.Sprintf(`(function(){
  try { return !!(%s); } catch (e) { return false; }
})()`, fnExpr)
}

func buildClickSelectorExpression(selector string) string {
	selJSON, _ := json.Marshal(selector)
	return fmt.Sprintf(`(function(){
  %s
  var sel = %s;
  var el = findFirstDeep(sel, document);
  if (!el) return false;
  if (el.scrollIntoView) el.scrollIntoView({block:'center', inline:'center'});
  if (el.click) { el.click(); return true; }
  var ev = new MouseEvent('click', {bubbles:true, cancelable:true});
  return el.dispatchEvent(ev);
})()`, buildDOMSearchHelpers(), string(selJSON))
}

func buildClickRefExpression(ref string) string {
	refJSON, _ := json.Marshal(ref)
	return fmt.Sprintf(`(function(){
  %s
  var ref = %s;
  var el = resolveRefElement(ref);
  if (!el) return false;
  if (el.scrollIntoView) el.scrollIntoView({block:'center', inline:'center'});
  if (el.click) { el.click(); return true; }
  var ev = new MouseEvent('click', {bubbles:true, cancelable:true});
  return el.dispatchEvent(ev);
})()`, buildDOMSearchHelpers(), string(refJSON))
}

func buildCallFunctionOnClickExpression() string {
	return `function(){
  if (!this) return false;
  if (this.scrollIntoView) this.scrollIntoView({block:'center', inline:'center'});
  if (this.click) { this.click(); return true; }
  var ev = new MouseEvent('click', {bubbles:true, cancelable:true});
  return this.dispatchEvent(ev);
}`
}

func buildFocusSelectorExpression(selector string, clear bool) string {
	selJSON, _ := json.Marshal(selector)
	clearJSON, _ := json.Marshal(clear)
	return fmt.Sprintf(`(function(){
  %s
  var sel = %s;
  var clear = %s;
  var el = findFirstDeep(sel, document);
  if (!el) return false;
  if (el.focus) el.focus();
  if (clear && ('value' in el)) {
    el.value = '';
    el.dispatchEvent(new Event('input', {bubbles:true}));
  }
  return true;
})()`, buildDOMSearchHelpers(), string(selJSON), string(clearJSON))
}

func buildFocusRefExpression(ref string, clear bool) string {
	refJSON, _ := json.Marshal(ref)
	clearJSON, _ := json.Marshal(clear)
	return fmt.Sprintf(`(function(){
  %s
  var ref = %s;
  var clear = %s;
  var el = resolveRefElement(ref);
  if (!el) return false;
  if (el.focus) el.focus();
  if (clear && ('value' in el)) {
    el.value = '';
    el.dispatchEvent(new Event('input', {bubbles:true}));
  }
  return true;
})()`, buildDOMSearchHelpers(), string(refJSON), string(clearJSON))
}

func buildCallFunctionOnFocusExpression(clear bool) string {
	clearJSON, _ := json.Marshal(clear)
	return fmt.Sprintf(`function(){
  var clear = %s;
  if (!this) return false;
  if (this.focus) this.focus();
  if (clear && ('value' in this)) {
    this.value = '';
    this.dispatchEvent(new Event('input', {bubbles:true}));
  }
  return true;
}`, string(clearJSON))
}

func buildSetValueSelectorExpression(selector string, value string) string {
	selJSON, _ := json.Marshal(selector)
	valueJSON, _ := json.Marshal(value)
	return fmt.Sprintf(`(function(){
  %s
  var sel = %s;
  var val = %s;
  var el = findFirstDeep(sel, document);
  if (!el) return false;
  if ('value' in el) {
    el.value = val;
  } else {
    el.textContent = val;
  }
  el.dispatchEvent(new Event('input', {bubbles:true}));
  el.dispatchEvent(new Event('change', {bubbles:true}));
  if (el.blur) el.blur();
  return true;
})()`, buildDOMSearchHelpers(), string(selJSON), string(valueJSON))
}

func buildSetValueRefExpression(ref string, value string) string {
	refJSON, _ := json.Marshal(ref)
	valueJSON, _ := json.Marshal(value)
	return fmt.Sprintf(`(function(){
  %s
  var ref = %s;
  var val = %s;
  var el = resolveRefElement(ref);
  if (!el) return false;
  if ('value' in el) {
    el.value = val;
  } else {
    el.textContent = val;
  }
  el.dispatchEvent(new Event('input', {bubbles:true}));
  el.dispatchEvent(new Event('change', {bubbles:true}));
  if (el.blur) el.blur();
  return true;
})()`, buildDOMSearchHelpers(), string(refJSON), string(valueJSON))
}

func buildCallFunctionOnSetValueExpression(value string) string {
	valueJSON, _ := json.Marshal(value)
	return fmt.Sprintf(`function(){
  var val = %s;
  if (!this) return false;
  if ('value' in this) {
    this.value = val;
  } else {
    this.textContent = val;
  }
  this.dispatchEvent(new Event('input', {bubbles:true}));
  this.dispatchEvent(new Event('change', {bubbles:true}));
  if (this.blur) this.blur();
  return true;
}`, string(valueJSON))
}

func buildResolveElementByMetaExpression(meta *ariaRefMeta) string {
	if meta == nil {
		return `(function(){ return null; })()`
	}
	metaJSON, _ := json.Marshal(meta)
	return fmt.Sprintf(`(function(){
  %s
  var meta = %s || {};
  var expectedRole = normalizeRefText(meta.role || '');
  var expectedName = normalizeRefText(meta.name || '');
  var expectedNth = (typeof meta.nth === 'number' && meta.nth >= 0) ? meta.nth : 0;
  var matches = [];
  var nodes = collectAllDeep(document.body);
  for (var i = 0; i < nodes.length; i++) {
    var el = nodes[i];
    if (!(el instanceof Element)) continue;
    if (!isVisibleRefNode(el)) continue;
    if (expectedRole && roleOfRefNode(el) !== expectedRole) continue;
    if (expectedName && nameOfRefNode(el) !== expectedName) continue;
    matches.push(el);
  }
  if (!matches.length) return null;
  var idx = expectedNth < matches.length ? expectedNth : 0;
  return matches[idx] || null;
})()`, buildDOMSearchHelpers(), string(metaJSON))
}

func buildClickMetaExpression(meta *ariaRefMeta) string {
	return fmt.Sprintf(`(function(){
  var el = %s;
  if (!el) return false;
  if (el.scrollIntoView) el.scrollIntoView({block:'center', inline:'center'});
  if (el.click) { el.click(); return true; }
  var ev = new MouseEvent('click', {bubbles:true, cancelable:true});
  return el.dispatchEvent(ev);
})()`, buildResolveElementByMetaExpression(meta))
}

func buildFocusMetaExpression(meta *ariaRefMeta, clear bool) string {
	clearJSON, _ := json.Marshal(clear)
	return fmt.Sprintf(`(function(){
  var clear = %s;
  var el = %s;
  if (!el) return false;
  if (el.focus) el.focus();
  if (clear && ('value' in el)) {
    el.value = '';
    el.dispatchEvent(new Event('input', {bubbles:true}));
  }
  return true;
})()`, string(clearJSON), buildResolveElementByMetaExpression(meta))
}

func buildSetValueMetaExpression(meta *ariaRefMeta, value string) string {
	valueJSON, _ := json.Marshal(value)
	return fmt.Sprintf(`(function(){
  var val = %s;
  var el = %s;
  if (!el) return false;
  if ('value' in el) {
    el.value = val;
  } else {
    el.textContent = val;
  }
  el.dispatchEvent(new Event('input', {bubbles:true}));
  el.dispatchEvent(new Event('change', {bubbles:true}));
  if (el.blur) el.blur();
  return true;
})()`, string(valueJSON), buildResolveElementByMetaExpression(meta))
}

func buildFindAndClickTextExpression(text string, exact bool) string {
	textJSON, _ := json.Marshal(text)
	exactJSON, _ := json.Marshal(exact)
	return fmt.Sprintf(`(function(){
  %s
  var needle = %s;
  var exact = %s;
  function visible(el) {
    if (!el || !(el instanceof Element)) return false;
    var st = window.getComputedStyle(el);
    if (st.display === 'none' || st.visibility === 'hidden' || st.opacity === '0') return false;
    return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
  }
  var nodes = collectAllDeep(document.body);
  for (var i = 0; i < nodes.length; i++) {
    var el = nodes[i];
    if (!(el instanceof Element)) continue;
    var tag = el.tagName ? el.tagName.toLowerCase() : '';
    if (!/^(a|button|input|textarea|select|label|span|div)$/.test(tag) && !(el.getAttribute && el.getAttribute('role'))) continue;
    var txt = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
    if (!txt) continue;
    var match = exact ? (txt === needle) : (txt.indexOf(needle) >= 0);
    if (!match) continue;
    if (!visible(el)) continue;
    if (el.scrollIntoView) el.scrollIntoView({block:'center', inline:'center'});
    if (el.click) { el.click(); return true; }
    var ev = new MouseEvent('click', {bubbles:true, cancelable:true});
    if (el.dispatchEvent(ev)) return true;
  }
  return false;
})()`, buildDOMSearchHelpers(), string(textJSON), string(exactJSON))
}

func buildAppendToActiveElementExpression(text string) string {
	textJSON, _ := json.Marshal(text)
	return fmt.Sprintf(`(function(){
  var txt = %s;
  var el = document.activeElement;
  if (!el) return false;
  if ('value' in el) {
    el.value = (el.value || '') + txt;
    el.dispatchEvent(new Event('input', {bubbles:true}));
    return true;
  }
  el.textContent = (el.textContent || '') + txt;
  return true;
})()`, string(textJSON))
}

func buildAriaSnapshotExpression(selector string, interactive bool, compact bool, maxDepth int) string {
	selectorJSON, _ := json.Marshal(selector)
	interactiveJSON, _ := json.Marshal(interactive)
	compactJSON, _ := json.Marshal(compact)
	return fmt.Sprintf(`(function(){
  %s
  var sel = %s;
  var interactiveOnly = %s;
  var compact = %s;
  var maxDepth = %d;
  window.__ariaRefs = {};
  window.__ariaRefMeta = { __url: location.href, __createdAt: Date.now() };
  var refCounter = 0;
  var refOrdinalByKey = {};
  var lines = [];

  function clampText(raw, limit) {
    raw = (raw || '').replace(/\s+/g, ' ').trim();
    if (raw.length > limit) return raw.slice(0, limit) + '...';
    return raw;
  }

  function textOf(el) {
    if (!el) return '';
    if (el.getAttribute) {
      var aria = el.getAttribute('aria-label');
      if (aria) return clampText(aria, 80);
      var labelled = el.getAttribute('aria-labelledby');
      if (labelled) {
        var name = labelled.split(/\s+/).map(function(id) {
          var node = document.getElementById(id);
          return node ? (node.innerText || node.textContent || '') : '';
        }).join(' ');
        if (name.trim()) return clampText(name, 80);
      }
      var alt = el.getAttribute('alt');
      if (alt) return clampText(alt, 80);
      var placeholder = el.getAttribute('placeholder');
      if (placeholder) return clampText(placeholder, 80);
    }
    if ('value' in el && typeof el.value === 'string' && el.value.trim()) {
      return clampText(el.value, 80);
    }
    return clampText(el.innerText || el.textContent || '', 80);
  }

  function roleOf(el) {
    if (!el || !el.tagName) return 'node';
    var explicitRole = el.getAttribute && el.getAttribute('role');
    if (explicitRole) return explicitRole.toLowerCase();
    var tag = el.tagName.toLowerCase();
    if (tag === 'a' && el.getAttribute && el.getAttribute('href')) return 'link';
    if (tag === 'button') return 'button';
    if (tag === 'textarea') return 'textbox';
    if (tag === 'select') return 'combobox';
    if (tag === 'option') return 'option';
    if (tag === 'img') return 'img';
    if (tag === 'main') return 'main';
    if (tag === 'nav') return 'navigation';
    if (tag === 'header') return 'banner';
    if (tag === 'footer') return 'contentinfo';
    if (tag === 'form') return 'form';
    if (tag === 'dialog') return 'dialog';
    if (/^h[1-6]$/.test(tag)) return 'heading';
    if (tag === 'input') {
      var type = (el.getAttribute('type') || 'text').toLowerCase();
      if (type === 'button' || type === 'submit' || type === 'reset') return 'button';
      if (type === 'checkbox') return 'checkbox';
      if (type === 'radio') return 'radio';
      if (type === 'range') return 'slider';
      return 'textbox';
    }
    return tag;
  }

  function isVisible(el) {
    if (!el || !(el instanceof Element)) return false;
    var style = window.getComputedStyle(el);
    if (!style) return true;
    if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false;
    return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length || (el.shadowRoot && el.shadowRoot.childNodes.length));
  }

  function isInteractive(el, role) {
    if (!el || !el.tagName) return false;
    var tag = el.tagName.toLowerCase();
    if (tag === 'a' && el.getAttribute && el.getAttribute('href')) return true;
    if (/^(button|input|textarea|select|option|summary)$/.test(tag)) return true;
    if (el.hasAttribute && el.hasAttribute('contenteditable')) return true;
    if (typeof el.tabIndex === 'number' && el.tabIndex >= 0) return true;
    if (el.onclick || (el.getAttribute && el.getAttribute('onclick'))) return true;
    return /^(button|link|textbox|checkbox|radio|switch|tab|menuitem|combobox|option|slider)$/.test(role);
  }

  function isSemantic(el, role) {
    if (!el || !el.tagName) return false;
    var tag = el.tagName.toLowerCase();
    if (/^h[1-6]$/.test(tag)) return true;
    return /^(main|navigation|banner|contentinfo|form|dialog|heading|article|region|list|listitem)$/.test(role);
  }

  function assignRef(el, role, name, tag) {
    refCounter += 1;
    var ref = 'e' + refCounter;
    window.__ariaRefs[ref] = el;
    var key = (role || '') + '|' + (name || '');
    var nth = refOrdinalByKey[key] || 0;
    refOrdinalByKey[key] = nth + 1;
    window.__ariaRefMeta[ref] = {
      role: role || '',
      name: name || '',
      nth: nth,
      tag: tag || '',
      id: el.id || ''
    };
    return ref;
  }

  function describe(el) {
    var role = roleOf(el);
    var name = textOf(el);
    var tag = el.tagName ? el.tagName.toLowerCase() : 'node';
    var extras = [];
    if (!compact) {
      extras.push('tag=' + tag);
      if (el.id) extras.push('id=' + el.id);
      if (el.getAttribute) {
        var href = el.getAttribute('href');
        if (href) extras.push('href=' + href);
      }
    }
    var ref = assignRef(el, role, name, tag);
    var line = '- ' + role + ' "' + name + '" [ref=' + ref + ']';
    if (extras.length) line += ' (' + extras.join(', ') + ')';
    return line;
  }

  function walk(node, depth) {
    if (!node || depth > maxDepth) return;
    var children = [];
    if (node instanceof Element) {
      if (!isVisible(node) && depth > 0) return;
      var role = roleOf(node);
      var include = depth === 0 || (interactiveOnly ? isInteractive(node, role) : (isInteractive(node, role) || isSemantic(node, role)));
      if (include) {
        var indent = new Array(depth + 1).join('  ');
        lines.push(indent + describe(node));
      }
      if (node.shadowRoot) {
        children = children.concat(Array.prototype.slice.call(node.shadowRoot.children || []));
      }
      children = children.concat(Array.prototype.slice.call(node.children || []));
    } else if (node instanceof ShadowRoot || node instanceof DocumentFragment) {
      children = children.concat(Array.prototype.slice.call(node.children || []));
    }
    for (var i = 0; i < children.length; i++) {
      walk(children[i], depth + 1);
    }
  }

  var root = document.body;
  if (sel && sel.trim()) {
    root = findFirstDeep(sel, document);
  }
  if (!root) return '- document "' + clampText(document.title || '', 120) + '"';

  var title = clampText(document.title || '', 120);
  lines.push('- document "' + title + '"');
  walk(root, 1);
  return lines.slice(0, 500).join('\n');
})()`, buildDOMSearchHelpers(), string(selectorJSON), string(interactiveJSON), string(compactJSON), maxDepth)
}
