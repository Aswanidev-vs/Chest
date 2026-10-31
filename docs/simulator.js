(function () {
  'use strict';

  var presets = {
    sort: {
      command: 'chest sort ~/Downloads --dry-run',
      lines: [
        ['key', 'CHEST PLAN'], ['dim', '# dry run · no files will move'], ['blank'],
        ['path', 'anime_ep01.mkv'], ['arrow', '→'], ['value', 'Downloads/Videos/anime_ep01.mkv'],
        ['path', 'profile_shot.png'], ['arrow', '→'], ['value', 'Downloads/Images/profile_shot.png'],
        ['path', 'q3_financials.pdf'], ['arrow', '→'], ['value', 'Downloads/Documents/q3_financials.pdf'],
        ['blank'], ['ok', '3 files prepared · 0 collisions · 0 changes made']
      ]
    },
    search: {
      command: 'chest search "revenue growth" --extension pdf',
      lines: [['ok', 'Found 1 match across 1,420 indexed files'], ['blank'], ['key', '[Documents]'], ['path', 'q3_financials.pdf'], ['dim', '2.4 MB · line 42'], ['value', 'Q3 net revenue growth exceeded forecast by +32%']]
    },
    stats: {
      command: 'chest stats ~/Projects',
      lines: [['key', 'CHEST STORAGE REPORT'], ['blank'], ['value', 'Files indexed        14,802'], ['value', 'Total size           82.4 GB'], ['value', 'Largest category     Developer  41.8 GB'], ['dim', 'Indexed locally · refreshed 0.8s ago']]
    },
    doctor: {
      command: 'chest doctor',
      lines: [['key', 'CHEST DOCTOR'], ['blank'], ['ok', 'PASS  index freshness'], ['ok', 'PASS  undo history'], ['ok', 'PASS  protected paths'], ['ok', 'PASS  scheduled jobs'], ['blank'], ['value', '4 checks passed · no action required']]
    },
    undo: {
      command: 'chest undo 14',
      lines: [['key', 'CHEST UNDO'], ['blank'], ['warn', 'Rehearsing rollback for Run #14'], ['arrow', '↩'], ['value', 'Downloads/Videos/anime_ep01.mkv  →  ~/Downloads/anime_ep01.mkv'], ['arrow', '↩'], ['value', 'Downloads/Images/profile_shot.png  →  ~/Downloads/profile_shot.png'], ['blank'], ['ok', '2 files would return to their original locations']]
    }
  };

  document.addEventListener('DOMContentLoaded', function () {
    if (window.lucide) window.lucide.createIcons();
    var form = document.getElementById('command-form');
    var input = document.getElementById('command-input');
    var terminal = document.getElementById('sim-terminal');
    var status = document.getElementById('output-status');
    var choices = document.querySelectorAll('.command-choice');
    var runId = 0;

    function render(kind, command) {
      var preset = presets[kind] || presets.sort;
      var currentRun = ++runId;
      status.textContent = 'running';
      terminal.classList.add('is-running');
      var commandLine = document.createElement('div');
      commandLine.className = 'terminal-line terminal-command';
      commandLine.innerHTML = '<span class="terminal-prompt">$</span><span>' + escapeHTML(command) + '</span>';
      terminal.replaceChildren(commandLine);
      window.setTimeout(function () {
        if (currentRun !== runId) return;
        preset.lines.forEach(function (line) {
          var row = document.createElement('div');
          row.className = 'terminal-line ' + (line[0] === 'blank' ? 'terminal-blank' : 'terminal-' + line[0]);
          row.textContent = line[0] === 'blank' ? '' : line[1];
          terminal.appendChild(row);
        });
        terminal.classList.remove('is-running');
        status.textContent = 'rehearsal complete';
      }, 420);
    }

    choices.forEach(function (choice) {
      choice.addEventListener('click', function () {
        choices.forEach(function (item) { item.classList.remove('is-active'); });
        choice.classList.add('is-active');
        input.value = presets[choice.dataset.command].command;
        render(choice.dataset.command, input.value);
      });
    });
    form.addEventListener('submit', function (event) {
      event.preventDefault();
      var match = input.value.match(/^\s*chest\s+(\w+)/i);
      var kind = match && presets[match[1].toLowerCase()] ? match[1].toLowerCase() : 'sort';
      render(kind, input.value.trim() || presets[kind].command);
    });
    document.getElementById('clear-output').addEventListener('click', function () {
      terminal.innerHTML = '<div class="terminal-welcome"><span class="terminal-mark">CHEST</span><strong>Output cleared</strong><span>Choose a command to start another rehearsal.</span></div>';
      status.textContent = 'ready';
    });
    render('sort', input.value);
  });

  function escapeHTML(value) {
    return value.replace(/[&<>'"]/g, function (character) { return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[character]; });
  }
}());
