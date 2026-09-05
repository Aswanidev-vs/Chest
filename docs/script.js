// CHEST docs — script.js v2
// Direction: TERMINAL-AS-ROOM
// Source of truth: docs/DESIGN.md §6

(function () {
  'use strict';

  document.addEventListener('DOMContentLoaded', function () {
    initHeroTyping();
    initCopyButtons();
    initInstallTabs();
    initSidebarTracking();
    initAnchorSmoothing();
    initNavActiveState();
  });

  // ---- 1. Hero terminal typing animation ----
  function initHeroTyping() {
    var pre = document.getElementById('terminal-body');
    if (!pre) return;

    var reduced = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    var fullText = pre.getAttribute('data-text') || pre.innerHTML;
    pre.innerHTML = '';

    if (reduced) {
      pre.innerHTML = fullText;
      return;
    }

    // If user has already seen this animation, skip.
    try {
      if (window.localStorage && localStorage.getItem('chest-typed') === '1') {
        pre.innerHTML = fullText;
        pre.classList.add('terminal-typed');
        return;
      }
    } catch (_) { /* localStorage may be unavailable */ }

    // Walk the full HTML string one character at a time, but skip anything
    // inside an HTML tag. This lets us preserve the rich token spans while
    // still showing the typing effect on visible characters.
    var i = 0;
    var len = fullText.length;
    var buf = '';
    var inside = false;

    function step() {
      // Advance until we've consumed at least 1 visible char, or hit end.
      var advanced = 0;
      while (i < len && advanced < 3) {
        var ch = fullText.charAt(i);
        if (ch === '<') { inside = true; }
        buf += ch;
        i += 1;
        if (ch === '>') { inside = false; advanced += 1; continue; }
        if (!inside) { advanced += 1; }
      }
      pre.innerHTML = buf;

      if (i < len) {
        setTimeout(step, 18 + Math.random() * 28);
      } else {
        // Done. Mark as seen, and replace the caret with a static one
        // (the CSS .terminal-typed class hides the blinking one).
        try { if (window.localStorage) localStorage.setItem('chest-typed', '1'); } catch (_) {}
        pre.classList.add('terminal-typed');
      }
    }

    setTimeout(step, 220);
  }

  // ---- 2. Copy buttons for the install lines ----
  function initCopyButtons() {
    var btns = document.querySelectorAll('[data-copy-target]');
    btns.forEach(function (btn) {
      btn.addEventListener('click', function () {
        var row = btn.parentElement;
        var src = row && row.querySelector('[data-copy]');
        if (!src) return;
        var text = src.getAttribute('data-copy') || src.textContent.trim();

        var done = function () {
          var orig = btn.textContent;
          btn.textContent = 'copied';
          btn.classList.add('copied');
          setTimeout(function () {
            btn.textContent = orig;
            btn.classList.remove('copied');
          }, 1400);
        };

        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(done, fallback);
        } else {
          fallback();
        }

        function fallback() {
          var ta = document.createElement('textarea');
          ta.value = text;
          ta.style.position = 'fixed';
          ta.style.opacity = '0';
          document.body.appendChild(ta);
          ta.select();
          try { document.execCommand('copy'); done(); } catch (_) {}
          document.body.removeChild(ta);
        }
      });
    });
  }

  // ---- 2b. Install tabs ----
  function initInstallTabs() {
    var container = document.querySelector('.install');
    if (!container) return;

    var tabs   = container.querySelectorAll('.install-tab');
    var panes  = container.querySelectorAll('.install-pane');
    if (!tabs.length || !panes.length) return;

    function activate(name) {
      tabs.forEach(function (t) {
        var on = t.getAttribute('data-tab') === name;
        t.classList.toggle('is-active', on);
        t.setAttribute('aria-selected', on ? 'true' : 'false');
      });
      panes.forEach(function (p) {
        var on = p.getAttribute('data-pane') === name;
        p.classList.toggle('is-active', on);
        if (on) p.removeAttribute('hidden'); else p.setAttribute('hidden', '');
      });
    }

    tabs.forEach(function (t) {
      t.addEventListener('click', function () { activate(t.getAttribute('data-tab')); });
      t.addEventListener('keydown', function (e) {
        if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
        e.preventDefault();
        var dir = e.key === 'ArrowRight' ? 1 : -1;
        var i = Array.prototype.indexOf.call(tabs, t);
        var next = tabs[(i + dir + tabs.length) % tabs.length];
        next.focus();
        activate(next.getAttribute('data-tab'));
      });
    });
  }

  // ---- 3. Sidebar active-link tracking ----
  function initSidebarTracking() {
    var sideLinks = document.querySelectorAll('.docs-side a[href^="#"]');
    if (!sideLinks.length) return;

    var sections = [];
    sideLinks.forEach(function (link) {
      var id = link.getAttribute('href').slice(1);
      var el = document.getElementById(id);
      if (el) sections.push({ id: id, el: el, link: link });
    });
    if (!sections.length) return;

    function update() {
      var fromTop = window.scrollY + 120;
      var current = null;
      for (var i = 0; i < sections.length; i++) {
        if (sections[i].el.offsetTop <= fromTop) current = sections[i];
        else break;
      }
      sideLinks.forEach(function (l) { l.classList.remove('is-active'); });
      if (current) current.link.classList.add('is-active');
    }

    var raf = null;
    window.addEventListener('scroll', function () {
      if (raf) return;
      raf = window.requestAnimationFrame(function () { update(); raf = null; });
    }, { passive: true });
    update();
  }

  // ---- 4. Smooth scroll for in-page anchors ----
  function initAnchorSmoothing() {
    var anchors = document.querySelectorAll('a[href^="#"]');
    anchors.forEach(function (a) {
      a.addEventListener('click', function (e) {
        var id = a.getAttribute('href').slice(1);
        if (!id) return;
        var target = document.getElementById(id);
        if (!target) return;
        e.preventDefault();
        var top = target.getBoundingClientRect().top + window.scrollY - 64;
        window.scrollTo({ top: top, behavior: 'smooth' });
        history.pushState(null, '', '#' + id);
      });
    });
  }

  // ---- 5. Top-nav active state (landing) ----
  function initNavActiveState() {
    var navLinks = document.querySelectorAll('.navbar .nav-link[href^="#"]');
    if (!navLinks.length) return;

    var pairs = [];
    navLinks.forEach(function (l) {
      var id = l.getAttribute('href').slice(1);
      var el = document.getElementById(id);
      if (el) pairs.push({ el: el, link: l });
    });
    if (!pairs.length) return;

    function update() {
      var fromTop = window.scrollY + 140;
      var current = null;
      for (var i = 0; i < pairs.length; i++) {
        if (pairs[i].el.offsetTop <= fromTop) current = pairs[i];
        else break;
      }
      navLinks.forEach(function (l) { l.classList.remove('is-active'); });
      if (current) current.link.classList.add('is-active');
    }
    var raf = null;
    window.addEventListener('scroll', function () {
      if (raf) return;
      raf = window.requestAnimationFrame(function () { update(); raf = null; });
    }, { passive: true });
    update();
  }
})();
