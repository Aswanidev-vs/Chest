/* ========================================================================
   CHEST CLI — Modern Interactive Script
   Includes Lucide CDN icon initialization, interactive terminal demo,
   multi-platform install tabs, and copy-to-clipboard interactions.
   Zero emojis.
   ======================================================================== */

(function () {
  'use strict';

  // Terminal commands and simulated output presets
  var TERMINAL_DEMOS = {
    sort: {
      title: 'bash — ~/Downloads (chest sort)',
      html: '<span class="term-ln"><span class="t-prompt">$</span> <span class="t-cmd">chest</span> <span class="t-cmd">sort</span> <span class="t-path">~/Downloads</span> <span class="t-flag">-p</span> downloads <span class="t-flag">--dry-run</span></span>' +
            '<span class="term-ln"></span>' +
            '<span class="term-ln"><span class="t-key">CHEST PLAN</span>  <span class="t-dim"># 3 moves, 0 collisions detected</span></span>' +
            '<span class="term-ln">  <span class="t-cmd">anime_ep01.mkv</span>      <span class="t-arrow">→</span>  <span class="t-path">Downloads/Videos/</span></span>' +
            '<span class="term-ln">  <span class="t-cmd">profile_shot.png</span>    <span class="t-arrow">→</span>  <span class="t-path">Downloads/Images/</span></span>' +
            '<span class="term-ln">  <span class="t-cmd">q3_financials.pdf</span>   <span class="t-arrow">→</span>  <span class="t-path">Downloads/Documents/</span></span>' +
            '<span class="term-ln"></span>' +
            '<span class="term-ln"><span class="t-warn">3 files prepared to move. No changes made (dry run).</span></span>' +
            '<span class="term-ln"><span class="t-dim">Run with -y or omit --dry-run to apply.</span></span>'
    },
    search: {
      title: 'bash — ~/Documents (chest search)',
      html: '<span class="term-ln"><span class="t-prompt">$</span> <span class="t-cmd">chest</span> <span class="t-cmd">search</span> <span class="t-flag">-e</span> pdf <span class="t-flag">-c</span> <span class="t-path">"revenue growth"</span></span>' +
            '<span class="term-ln"></span>' +
            '<span class="term-ln"><span class="t-ok">✓ Found 1 match</span> across 1,420 files <span class="t-dim">(4.2ms)</span></span>' +
            '<span class="term-ln">  <span class="t-key">[Documents]</span> <span class="t-cmd">q3_financials.pdf</span> <span class="t-dim">(2.4 MB)</span></span>' +
            '<span class="term-ln">    <span class="t-dim">Line 42:</span> "Q3 net revenue growth exceeded forecast by <span class="t-ok">+32%</span>..."</span>'
    },
    watch: {
      title: 'bash — ~/Downloads (chest watch daemon)',
      html: '<span class="term-ln"><span class="t-prompt">$</span> <span class="t-cmd">chest</span> <span class="t-cmd">watch</span> <span class="t-path">~/Downloads</span> <span class="t-flag">--preset</span> media <span class="t-flag">--initial</span></span>' +
            '<span class="term-ln"></span>' +
            '<span class="term-ln"><span class="t-ok">● Active watcher</span> on <span class="t-path">~/Downloads</span></span>' +
            '<span class="term-ln"><span class="t-dim">Sorting existing files first (--initial)...</span></span>' +
            '<span class="term-ln">  <span class="t-ok">📁 Folder created:</span> <span class="t-cmd">Screenshots</span></span>' +
            '<span class="term-ln">  <span class="t-ok">⚡ Organized:</span> <span class="t-cmd">recording_2026.mp4</span> <span class="t-arrow">→</span> <span class="t-path">~/Downloads/Videos/</span></span>' +
            '<span class="term-ln">  <span class="t-warn">🗑 Folder removed:</span> <span class="t-cmd">old-takes</span></span>'
    },
    completion: {
      title: 'bash — one-time shell setup (chest completion)',
      html: '<span class="term-ln"><span class="t-prompt">$</span> <span class="t-cmd">chest</span> <span class="t-cmd">completion</span> <span class="t-flag">--install</span></span>' +
            '<span class="term-ln"></span>' +
            '<span class="term-ln"><span class="t-ok">  Added to ~/.bashrc</span></span>' +
            '<span class="term-ln"><span class="t-ok">  Installed bash completion -> ~/.local/share/bash-completion/completions/chest</span></span>' +
            '<span class="term-ln"><span class="t-dim">  Restart your shell, then type `chest ` and press Tab to try it.</span></span>' +
            '<span class="term-ln"></span>' +
            '<span class="term-ln"><span class="t-prompt">$</span> <span class="t-cmd">chest</span> so<span class="t-key">▮</span>   <span class="t-dim"># Tab → chest sort</span></span>'
    },
    duplicates: {
      title: 'bash — ~/Projects (chest duplicates)',
      html: '<span class="term-ln"><span class="t-prompt">$</span> <span class="t-cmd">chest</span> <span class="t-cmd">duplicates</span> <span class="t-path">~/Projects</span></span>' +
            '<span class="term-ln"></span>' +
            '<span class="term-ln"><span class="t-ok">Scanned 14,802 files</span> with SHA-256 hash comparison</span>' +
            '<span class="term-ln"><span class="t-warn">2 duplicate sets found (Wasting 142 MB):</span></span>' +
            '<span class="term-ln">  <span class="t-key">Hash:</span> a8f3d1...9c4</span>' +
            '<span class="term-ln">    - <span class="t-path">app/assets/bundle.js</span> <span class="t-dim">(71 MB)</span></span>' +
            '<span class="term-ln">    - <span class="t-path">backup/old_bundle.js</span> <span class="t-dim">(71 MB)</span></span>'
    },
    speedtest: {
      title: 'bash — network diagnostics (chest speedtest)',
      html: '<span class="term-ln"><span class="t-prompt">$</span> <span class="t-cmd">chest</span> <span class="t-cmd">speedtest</span></span>' +
            '<span class="term-ln"></span>' +
            '<span class="term-ln"><span class="t-key">CHEST SPEED TEST</span> <span class="t-dim"># Ookla vs Cloudflare</span></span>' +
            '<span class="term-ln"><span class="t-key">Ookla</span></span>' +
            '<span class="term-ln">  <span class="t-cmd">download</span> <span class="t-ok">800.00 Mbps</span>  <span class="t-key">[◆◆◆◆◆◆◆◆◆◆◆◆◆◆◆◆]</span></span>' +
            '<span class="term-ln">  <span class="t-cmd">upload</span>   <span class="t-ok">200.00 Mbps</span>  <span class="t-key">[◆◆◆◆◇◇◇◇◇◇◇◇◇◇◇◇]</span></span>' +
            '<span class="term-ln"><span class="t-key">Cloudflare</span></span>' +
            '<span class="term-ln">  <span class="t-cmd">download</span> <span class="t-ok">400.00 Mbps</span>  <span class="t-key">[◆◆◆◆◆◆◆◆◇◇◇◇◇◇◇◇]</span></span>' +
            '<span class="term-ln">  <span class="t-cmd">upload</span>   <span class="t-ok">100.00 Mbps</span>  <span class="t-key">[◆◆◇◇◇◇◇◇◇◇◇◇◇◇◇◇]</span></span>'
    },
    undo: {
      title: 'bash — ~/Downloads (chest undo)',
      html: '<span class="term-ln"><span class="t-prompt">$</span> <span class="t-cmd">chest</span> <span class="t-cmd">undo</span></span>' +
            '<span class="term-ln"></span>' +
            '<span class="term-ln"><span class="t-ok">Reverting Run #14</span> logged at 16:14:02 UTC</span>' +
            '<span class="term-ln">  <span class="t-arrow">↩</span> <span class="t-path">Downloads/Videos/anime_ep01.mkv</span> <span class="t-arrow">→</span> <span class="t-path">~/Downloads/anime_ep01.mkv</span></span>' +
            '<span class="term-ln">  <span class="t-arrow">↩</span> <span class="t-path">Downloads/Images/profile_shot.png</span> <span class="t-arrow">→</span> <span class="t-path">~/Downloads/profile_shot.png</span></span>' +
            '<span class="term-ln"><span class="t-ok">✓ 2 files reverted to original locations. Directory restored.</span></span>'
    }
  };

  document.addEventListener('DOMContentLoaded', function () {
    initLucideIcons();
    initInstallTabs();
    initTerminalDemo();
    initCopyButtons();
    initSmoothScroll();
    initSidebarTracking();
  });

  // 1. Initialize Lucide Icons via CDN
  function initLucideIcons() {
    if (window.lucide && typeof window.lucide.createIcons === 'function') {
      window.lucide.createIcons();
    }
  }

  // 2. Install Tabs Switching
  function initInstallTabs() {
    var tabs = document.querySelectorAll('.install-tab-btn');
    var cmdEl = document.getElementById('install-cmd-text');
    var captionEl = document.getElementById('install-caption-text');
    var promptSym = document.getElementById('install-prompt-symbol');
    if (!tabs.length || !cmdEl) return;

    var installData = {
      go: {
        symbol: '$',
        cmd: 'go install github.com/Aswanidev-vs/chest/cmd/chest@latest',
        caption: 'Requires Go >= 1.22. Direct compile into $(go env GOBIN) or $(go env GOPATH)/bin.'
      },
      unix: {
        symbol: '$',
        cmd: 'curl -sSL https://raw.githubusercontent.com/Aswanidev-vs/chest/main/install.sh | bash',
        caption: 'Automated installer for Linux & macOS. Verifies Go, builds binary, and configures PATH.'
      },
      win: {
        symbol: '>',
        cmd: 'irm https://raw.githubusercontent.com/Aswanidev-vs/chest/main/install.ps1 | iex',
        caption: 'Automated PowerShell installer for Windows 10/11 & PowerShell 7+.'
      }
    };

    tabs.forEach(function (btn) {
      btn.addEventListener('click', function () {
        var key = btn.getAttribute('data-tab');
        if (!installData[key]) return;

        tabs.forEach(function (t) { t.classList.remove('active'); });
        btn.classList.add('active');

        var data = installData[key];
        cmdEl.textContent = data.cmd;
        if (captionEl) captionEl.textContent = data.caption;
        if (promptSym) promptSym.textContent = data.symbol;

        var copyBtn = document.getElementById('install-copy-btn');
        if (copyBtn) {
          copyBtn.setAttribute('data-copy', data.cmd);
        }
      });
    });
  }

  // 3. Interactive Terminal Demo Switcher
  function initTerminalDemo() {
    var chips = document.querySelectorAll('.term-cmd-chip');
    var body = document.getElementById('term-demo-body');
    var title = document.getElementById('term-demo-title');
    if (!chips.length || !body) return;

    chips.forEach(function (chip) {
      chip.addEventListener('click', function () {
        var cmd = chip.getAttribute('data-cmd');
        var demo = TERMINAL_DEMOS[cmd];
        if (!demo) return;

        chips.forEach(function (c) { c.classList.remove('active'); });
        chip.classList.add('active');

        if (title) title.textContent = demo.title;
        body.style.opacity = '0';
        setTimeout(function () {
          body.innerHTML = demo.html;
          body.style.opacity = '1';
        }, 120);
      });
    });
  }

  // 4. Copy-to-Clipboard with icon animation
  function initCopyButtons() {
    var buttons = document.querySelectorAll('[data-copy-btn]');
    buttons.forEach(function (btn) {
      btn.addEventListener('click', function () {
        var text = btn.getAttribute('data-copy');
        if (!text) {
          var targetId = btn.getAttribute('data-copy-target');
          if (targetId) {
            var target = document.getElementById(targetId);
            if (target) text = target.textContent.trim();
          }
        }
        if (!text) {
          var wrapper = btn.closest('.code-block-wrapper');
          if (wrapper) {
            var codeEl = wrapper.querySelector('code, pre');
            if (codeEl) {
              // Copy text without leading terminal prompt symbols
              text = codeEl.innerText.replace(/^[>$]\s+/gm, '').trim();
            }
          }
        }
        if (!text) return;

        navigator.clipboard.writeText(text).then(function () {
          var origHTML = btn.innerHTML;
          btn.classList.add('copied');
          btn.innerHTML = '<i data-lucide="check"></i> <span>Copied!</span>';
          initLucideIcons();

          setTimeout(function () {
            btn.classList.remove('copied');
            btn.innerHTML = origHTML;
            initLucideIcons();
          }, 1800);
        });
      });
    });
  }

  // 5. Smooth Scroll for Anchor Links
  function initSmoothScroll() {
    var links = document.querySelectorAll('a[href^="#"]');
    links.forEach(function (link) {
      link.addEventListener('click', function (e) {
        var id = link.getAttribute('href').slice(1);
        if (!id) return;
        var target = document.getElementById(id);
        if (target) {
          e.preventDefault();
          var offset = 80;
          var top = target.getBoundingClientRect().top + window.scrollY - offset;
          window.scrollTo({ top: top, behavior: 'smooth' });
        }
      });
    });
  }

  // 6. Sidebar active item tracking on docs page
  function initSidebarTracking() {
    var sideLinks = document.querySelectorAll('.docs-nav-link[href^="#"]');
    if (!sideLinks.length) return;

    var headings = [];
    sideLinks.forEach(function (link) {
      var id = link.getAttribute('href').slice(1);
      var el = document.getElementById(id);
      if (el) headings.push({ id: id, el: el, link: link });
    });

    window.addEventListener('scroll', function () {
      var top = window.scrollY + 100;
      var current = null;
      for (var i = 0; i < headings.length; i++) {
        if (headings[i].el.offsetTop <= top) {
          current = headings[i];
        } else {
          break;
        }
      }
      sideLinks.forEach(function (l) { l.classList.remove('is-active'); });
      if (current) current.link.classList.add('is-active');
    }, { passive: true });
  }

})();
